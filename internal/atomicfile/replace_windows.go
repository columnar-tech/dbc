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

package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// transientFor bounds how long replace and openForRead wait out other
// handles on the file. Readers and replacements hold it only briefly, so this
// is generous; cmd/go's robustio uses the same.
const transientFor = 2 * time.Second

// replace renames source over destination, which must be in the same
// directory. MoveFileEx refuses to replace a file that any handle has open,
// even one sharing delete access. os.Root's Rename uses POSIX rename semantics
// where the filesystem supports them (NTFS on Windows 10 1607 and later), which
// replace a file that is open with FILE_SHARE_DELETE, as openForRead's handles
// are; elsewhere it falls back to a plain replacing rename. A handle without
// FILE_SHARE_DELETE (any os.Open) still blocks it with ERROR_ACCESS_DENIED or
// ERROR_SHARING_VIOLATION, so replace waits those out.
func replace(source, destination string) error {
	dir := filepath.Dir(destination)
	if filepath.Dir(source) != dir {
		return fmt.Errorf("replace %s with %s: not in the same directory", destination, source)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return retryTransient(func() error {
		return root.Rename(filepath.Base(source), filepath.Base(destination))
	})
}

// openForRead opens path for reading with every share mode, including
// FILE_SHARE_DELETE, so a concurrent replace can rename over the file while it
// is open; the handle keeps reading the old contents, as on POSIX. With
// os.Open a reader that reopens in a loop could keep a writer out for good.
// It waits out a replace that briefly holds the file exclusively
// (ERROR_SHARING_VIOLATION, or ERROR_ACCESS_DENIED while the replaced file is
// pending deletion).
func openForRead(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var h windows.Handle
	err = retryTransient(func() (err error) {
		h, err = windows.CreateFile(name, windows.GENERIC_READ,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		return err
	})
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// retryTransient calls op until it succeeds, fails with a non-transient error,
// or transientFor passes, backing off up to 100ms between attempts. The last
// error is returned: ERROR_ACCESS_DENIED can also be permanent.
func retryTransient(op func() error) error {
	deadline := time.Now().Add(transientFor)
	for delay := time.Millisecond; ; delay = min(2*delay, 100*time.Millisecond) {
		err := op()
		if err == nil || !isTransient(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
	}
}

func isTransient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
