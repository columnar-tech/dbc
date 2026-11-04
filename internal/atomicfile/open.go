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

import "os"

// Open opens a file that WriteFilePreservingMode may be replacing
// concurrently, possibly from another process. On Windows, the open handle
// does not block a replacement (it keeps reading the old contents), and Open
// retries the sharing errors from a replacement in progress for a short time;
// elsewhere it is os.Open. A missing file is reported at once, as by os.Open.
func Open(path string) (*os.File, error) {
	return openForRead(path)
}
