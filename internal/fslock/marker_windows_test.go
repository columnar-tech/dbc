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

//go:build windows

package fslock

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// holdDeletePending creates marker and leaves it "delete pending", as a
// release does for the instant between marking the file for deletion and
// closing the handle. It uses the classic (non-POSIX) disposition, which keeps
// the name in place until the returned handle is closed, so the state lasts
// as long as the test needs.
func holdDeletePending(t *testing.T, marker string) windows.Handle {
	t.Helper()
	name, err := windows.UTF16PtrFromString(marker)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.CREATE_NEW, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	deleteFile := uint32(1) // FILE_DISPOSITION_INFO.DeleteFile
	if err := windows.SetFileInformationByHandle(h, windows.FileDispositionInfo,
		(*byte)(unsafe.Pointer(&deleteFile)), uint32(unsafe.Sizeof(deleteFile))); err != nil {
		windows.CloseHandle(h) //nolint:errcheck
		t.Fatal(err)
	}
	return h
}

// A delete that finishes within the grace must not fail the acquire, even a
// non-blocking one: the lock is free, just not yet creatable.
func TestAcquireMarkerWaitsOutDeletePending(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")
	marker := markerFor(t, lockPath)
	h := holdDeletePending(t, marker)
	go func() {
		time.Sleep(markerDeletePendingGrace / 5)
		windows.CloseHandle(h) //nolint:errcheck
	}()

	release, err := acquireMarker(t.Context(), marker, lockPath, 0, nil, nil)
	if err != nil {
		t.Fatalf("acquireMarker with a delete finishing within the grace: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

// An access-denied error that outlasts the grace is a real permission
// problem: it must be reported as one, and promptly rather than after the
// caller's whole timeout.
func TestAcquireMarkerReportsPersistentPermissionError(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")
	marker := markerFor(t, lockPath)
	h := holdDeletePending(t, marker)
	defer windows.CloseHandle(h) //nolint:errcheck

	const timeout = 10 * time.Second
	start := time.Now()
	_, err := acquireMarker(t.Context(), marker, lockPath, timeout, nil, nil)
	elapsed := time.Since(start)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("acquireMarker with a marker stuck delete-pending: err = %v, want fs.ErrPermission", err)
	}
	if elapsed > timeout/2 {
		t.Fatalf("acquireMarker took %s to report a persistent permission error; want about %s", elapsed, markerDeletePendingGrace)
	}
}
