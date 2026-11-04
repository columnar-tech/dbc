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
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// transientFor bounds how long replace and openForRead wait out other
// handles on the file. Readers and replacements hold it only briefly, so this
// is generous; cmd/go's robustio uses the same.
const transientFor = 2 * time.Second

// replace renames source over destination. Windows refuses to replace a file
// while another handle without FILE_SHARE_DELETE has it open, which includes
// every os.Open, failing with ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION.
func replace(source, destination string) error {
	src, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return retryTransient(func() error {
		return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	})
}

// openForRead opens path, waiting out a concurrent replace, which briefly holds
// the file exclusively (ERROR_SHARING_VIOLATION, or ERROR_ACCESS_DENIED while
// the replaced file is pending deletion).
func openForRead(path string) (f *os.File, err error) {
	err = retryTransient(func() error {
		f, err = os.Open(path)
		return err
	})
	return f, err
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
