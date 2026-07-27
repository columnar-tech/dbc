// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeLockPathUnwritable seeds a read-only file at the lock path so that
// opening it with O_RDWR fails with a permission error. This reproduces the
// open() failure a user hits when the install directory requires elevation
// (e.g. C:\Program Files\ADBC\Drivers for --level system) without the test
// needing to be elevated or to manipulate ACLs.
//
// Returns false if the platform/filesystem/user does not enforce the
// restriction, so callers can skip rather than assert on a non-reproduction.
func makeLockPathUnwritable(t *testing.T, lockPath string) bool {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Log("running as root/elevated: mode bits do not deny access")
		return false
	}
	require.NoError(t, os.WriteFile(lockPath, nil, 0o400))
	require.NoError(t, os.Chmod(lockPath, 0o400))
	t.Cleanup(func() { _ = os.Chmod(lockPath, 0o600) })

	// Confirm the restriction actually bites before relying on it.
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err == nil {
		f.Close()
		t.Log("filesystem does not enforce the read-only bit for this user")
		return false
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Logf("expected a permission error, got: %v", err)
		return false
	}
	return true
}

// TestAcquireLockPermissionErrorIsNotReportedAsContention is the regression
// test for the reported bug:
//
//	PS C:\Users\Bryce> dbc uninstall --level system foo
//	Error: another dbc operation is in progress: fslock: open
//	  C:\Program Files\ADBC\Drivers\.dbc.install.lock: ... Access is denied.
//
// No other dbc process was running. The install directory required
// Administrator rights, so creating the lock file failed with
// ERROR_ACCESS_DENIED — and every lock call site blanket-wrapped *any*
// Acquire failure as "another dbc operation is in progress". That sent the
// user hunting for a phantom concurrent process instead of telling them to
// elevate.
//
// Before the fix this test fails on the first assertion: the message contains
// "another dbc operation is in progress".
func TestAcquireLockPermissionErrorIsNotReportedAsContention(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".dbc.install.lock")
	if !makeLockPathUnwritable(t, lockPath) {
		t.Skip("cannot make lock path unwritable on this platform/user")
	}

	_, err := acquireLock(lockPath, 100*time.Millisecond)
	require.Error(t, err, "acquiring a lock in an unwritable location must fail")

	assert.NotContains(t, err.Error(), "another dbc operation is in progress",
		"a permission failure must NOT be reported as a concurrent dbc operation; "+
			"nothing was holding the lock")

	// The message must instead point at the real cause and the real remedy.
	assert.Contains(t, err.Error(), "permission denied",
		"error should name the actual cause")
	assert.Contains(t, err.Error(), dir,
		"error should name the directory that could not be written")
	assert.Contains(t, err.Error(), "elevated privileges",
		"error should explain that elevation is required")

	if runtime.GOOS == "windows" {
		assert.Contains(t, err.Error(), "Administrator",
			"on Windows the remedy is an Administrator terminal, not sudo")
		assert.NotContains(t, err.Error(), "sudo",
			"must not suggest sudo on Windows")
	} else {
		assert.Contains(t, err.Error(), "sudo",
			"on Unix the remedy is sudo")
	}
}

// TestAcquireLockGenuineContentionStillReported guards the other side of the
// fix: a real concurrent holder must still produce the "another dbc operation
// is in progress" message. A fix that simply dropped that message would make
// the test above pass while destroying the diagnostic it exists to provide.
func TestAcquireLockGenuineContentionStillReported(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".dbc.install.lock")

	held, err := fslock.Acquire(lockPath, 5*time.Second)
	require.NoError(t, err)
	defer held.Release() //nolint:errcheck

	_, err = acquireLock(lockPath, 100*time.Millisecond)
	require.Error(t, err, "lock is held, so acquisition must fail")
	assert.Contains(t, err.Error(), "another dbc operation is in progress",
		"genuine contention must still be reported as a concurrent operation")
}

// TestUninstallReportsElevationNotContention drives the actual `dbc uninstall`
// command path — the exact invocation from the bug report — rather than just
// the helper, so a regression at the call site is caught even if the helper
// stays correct. install/add/remove/sync share the same helper.
func TestUninstallReportsElevationNotContention(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADBC_DRIVER_PATH", dir)

	lockPath := filepath.Join(dir, ".dbc.install.lock")
	if !makeLockPathUnwritable(t, lockPath) {
		t.Skip("cannot make lock path unwritable on this platform/user")
	}

	m := UninstallCmd{Driver: "foo"}.GetModelCustom(testBaseModel())
	init, ok := m.(interface{ Init() tea.Cmd })
	require.True(t, ok)

	cmd := init.Init()
	require.NotNil(t, cmd)
	msg := cmd()

	err, isErr := msg.(error)
	require.True(t, isErr, "uninstall must surface an error, got %T: %v", msg, msg)

	assert.NotContains(t, err.Error(), "another dbc operation is in progress",
		"uninstall must not blame a phantom concurrent dbc process for a "+
			"permission failure")
	assert.True(t,
		strings.Contains(err.Error(), "elevated privileges"),
		"uninstall should tell the user to elevate; got: %v", err)
}
