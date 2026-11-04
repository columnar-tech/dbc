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
	"time"

	"golang.org/x/sys/windows"
)

// replaceRetryFor bounds how long replace waits for other handles on the
// destination to close. Readers (os.Open in this or another process) hold it
// only while reading, so this is generous; cmd/go's robustio uses the same.
const replaceRetryFor = 2 * time.Second

// replace renames source over destination. Windows refuses to replace a file
// while another handle without FILE_SHARE_DELETE has it open, which includes
// every os.Open, failing with ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION.
// Retry those with backoff until replaceRetryFor, then return the last error:
// ERROR_ACCESS_DENIED can also be permanent (a read-only destination).
func replace(source, destination string) error {
	src, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(replaceRetryFor)
	for delay := time.Millisecond; ; delay = min(2*delay, 100*time.Millisecond) {
		err = windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if err == nil || !isTransientReplaceError(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
	}
}

func isTransientReplaceError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
