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

package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadOnlyLockOpenErrorClassification(t *testing.T) {
	pathOpenPermission := &fs.PathError{Op: "open", Path: "lock", Err: fs.ErrPermission}
	if !readOnlyLockOpenError(pathOpenPermission) {
		t.Fatal("open permission error must permit read-only reuse fallback")
	}
	if !readOnlyLockOpenError(&fs.PathError{Op: "open", Path: "lock", Err: syscall.EROFS}) {
		t.Fatal("open EROFS must permit read-only reuse fallback")
	}

	for name, err := range map[string]error{
		"raw permission":                           fs.ErrPermission,
		"stat permission":                          &fs.PathError{Op: "stat", Path: "lock", Err: fs.ErrPermission},
		"lock contention":                          fmt.Errorf("lock: %w", fslock.ErrLockContended),
		"context cancellation":                     context.Canceled,
		"context timeout":                          context.DeadlineExceeded,
		"open permission joined with cancellation": errors.Join(pathOpenPermission, context.Canceled),
		"open permission joined with contention":   errors.Join(pathOpenPermission, fslock.ErrLockContended),
		"open collision":                           &fs.PathError{Op: "open", Path: "lock", Err: fs.ErrExist},
		"invalid lock path":                        errors.New("invalid lock path"),
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, readOnlyLockOpenError(err))
		})
	}
}

func TestCheckInstalledPackageReadOnlyFallback(t *testing.T) {
	requireReuseSupported(t)
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only mode-bit checks require a non-elevated Unix user")
	}

	for _, tc := range []struct {
		name string
		lock string
	}{
		{name: "read-only install root", lock: "root"},
		{name: "read-only driver lock", lock: "driver"},
		{name: "read-only namespace lock", lock: "namespace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
			verifierCalls := 0
			verifier := func(stage string, manifest Manifest) error {
				verifierCalls++
				assert.FileExists(t, filepath.Join(stage, manifest.Files.Driver))
				assert.FileExists(t, filepath.Join(stage, manifest.Files.Signature))
				return nil
			}
			digest := installReuseArchive(t, cfg, testPackageArchiveWithSignature(t, "new", "driver.detached"), InstallPackageOptions{SignatureVerifier: verifier})
			registered, err := GetDriver(cfg, "driver")
			require.NoError(t, err)
			expect := reuseExpectation(digest)
			expect.SignatureVerifier = verifier

			var driverLockPath, namespaceLockPath string
			driverLockPath, err = driverInstallLockPath(cfg.Location, "driver")
			require.NoError(t, err)
			namespaceLockPath, _, err = registrationNamespaceLockPath(cfg, cfg.Location)
			require.NoError(t, err)

			switch tc.lock {
			case "root":
				require.NoError(t, os.Chmod(cfg.Location, 0o555))
				t.Cleanup(func() { _ = os.Chmod(cfg.Location, 0o755) })
			case "driver":
				if !makeReuseLockReadOnly(t, driverLockPath) {
					t.Skip("filesystem does not enforce read-only lock file mode")
				}
			case "namespace":
				if !makeReuseLockReadOnly(t, namespaceLockPath) {
					t.Skip("filesystem does not enforce read-only lock file mode")
				}
			}

			beforeCalls := verifierCalls
			info, matched, err := CheckInstalledPackage(context.Background(), cfg, "driver", expect)
			require.NoError(t, err)
			assert.True(t, matched)
			assertSameResolvedRegistration(t, registered, info)
			assert.Greater(t, verifierCalls, beforeCalls, "signature verification must run on the read-only path")
		})
	}
}

func TestCheckInstalledPackageReadOnlyFallbackRevalidatesAfterVerifier(t *testing.T) {
	requireReuseSupported(t)
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only mode-bit checks require a non-elevated Unix user")
	}

	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, cfg Config, registered DriverInfo)
	}{
		{
			name: "library bytes changed",
			mutate: func(t *testing.T, _ Config, registered DriverInfo) {
				require.NoError(t, os.WriteFile(registered.Driver.Shared.Get(PlatformTuple()), []byte("external writer"), 0o755))
			},
		},
		{
			name: "library removed",
			mutate: func(t *testing.T, _ Config, registered DriverInfo) {
				require.NoError(t, os.Remove(registered.Driver.Shared.Get(PlatformTuple())))
			},
		},
		{
			name: "generation renamed",
			mutate: func(t *testing.T, _ Config, registered DriverInfo) {
				generation := filepath.Dir(registered.Driver.Shared.Get(PlatformTuple()))
				require.NoError(t, os.Rename(generation, generation+".renamed"))
			},
		},
		{
			name: "signature bytes changed",
			mutate: func(t *testing.T, _ Config, registered DriverInfo) {
				signature := filepath.Join(filepath.Dir(registered.Driver.Shared.Get(PlatformTuple())), "driver.detached")
				require.NoError(t, os.WriteFile(signature, []byte("changed signature"), 0o600))
			},
		},
		{
			name: "registration changed",
			mutate: func(t *testing.T, cfg Config, registered DriverInfo) {
				changedVersion := registered.Version.IncPatch()
				registered.Version = &changedVersion
				require.NoError(t, createDriverManifestUnlocked(cfg.Location, registered))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
			verifier := func(string, Manifest) error { return nil }
			digest := installReuseArchive(t, cfg, testPackageArchiveWithSignature(t, "new", "driver.detached"), InstallPackageOptions{SignatureVerifier: verifier})
			registered, err := GetDriver(cfg, "driver")
			require.NoError(t, err)
			driverLockPath, err := driverInstallLockPath(cfg.Location, "driver")
			require.NoError(t, err)
			if !makeReuseLockReadOnly(t, driverLockPath) {
				t.Skip("filesystem does not enforce read-only lock file mode")
			}

			callbackCalled := false
			expect := reuseExpectation(digest)
			expect.SignatureVerifier = func(string, Manifest) error {
				callbackCalled = true
				tc.mutate(t, cfg, registered)
				return nil
			}
			_, matched, err := CheckInstalledPackage(context.Background(), cfg, "driver", expect)
			require.NoError(t, err)
			assert.True(t, callbackCalled)
			assert.False(t, matched, "a stale optimistic snapshot must not be reused")
		})
	}
}

func TestCheckInstalledPackageReadOnlyFallbackDoesNotSwallowCancellation(t *testing.T) {
	requireReuseSupported(t)
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only mode-bit checks require a non-elevated Unix user")
	}
	cfg := Config{Level: ConfigEnv, Location: t.TempDir()}
	digest := installReuseArchive(t, cfg, testPackageArchiveWithSignature(t, "new", "driver.detached"), InstallPackageOptions{
		SignatureVerifier: func(string, Manifest) error { return nil },
	})
	driverLockPath, err := driverInstallLockPath(cfg.Location, "driver")
	require.NoError(t, err)
	if !makeReuseLockReadOnly(t, driverLockPath) {
		t.Skip("filesystem does not enforce read-only lock file mode")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	expect := reuseExpectation(digest)
	expect.SignatureVerifier = func(string, Manifest) error {
		cancel()
		return nil
	}
	_, matched, err := CheckInstalledPackage(ctx, cfg, "driver", expect)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, matched)
}

func makeReuseLockReadOnly(t *testing.T, path string) bool {
	t.Helper()
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		return false
	}
	require.NoError(t, os.Chmod(path, 0o444))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err == nil {
		_ = file.Close()
		return false
	}
	return errors.Is(err, fs.ErrPermission)
}
