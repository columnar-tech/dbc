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
	"os"
	"path/filepath"
	"testing"
	"time"
)

// os.Open does not share delete access, so on Windows a reader that has the
// destination open makes the replacement fail until it closes. A writer must
// wait for such a reader instead of failing.
func TestReplaceWaitsForReaderToClose(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "destination")
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		reader.Close()
	}()

	if err := WriteFilePreservingMode(destination, []byte("new"), 0o600); err != nil {
		t.Fatalf("replace while a reader had the file open: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("destination = %q, want %q", got, "new")
	}
}
