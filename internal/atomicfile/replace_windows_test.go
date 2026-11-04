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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
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

// A reader opened with Open must not block a writer: a reader that reopens in
// a loop could otherwise keep it out past any retry window. The replacement
// succeeds at once, and the open reader still sees the old contents.
func TestReplaceSucceedsWhileOpenHandleIsHeld(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "destination")
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	start := time.Now()
	if err := WriteFilePreservingMode(destination, []byte("new"), 0o600); err != nil {
		t.Fatalf("replace while an Open handle was held: %v", err)
	}
	if elapsed := time.Since(start); elapsed > transientFor/4 {
		t.Fatalf("replace took %s; an Open handle must not block it", elapsed)
	}
	old, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "old" {
		t.Fatalf("open reader read %q, want the old contents %q", old, "old")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("destination = %q, want %q", got, "new")
	}
}

// A replacement briefly holds the destination exclusively, so a reader must
// wait for it instead of failing with a sharing violation.
func TestOpenWaitsForExclusiveHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// Share mode 0: no other handle may open the file while this one is open.
	h, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		windows.CloseHandle(h)
	}()

	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open while another handle held the file exclusively: %v", err)
	}
	f.Close()
}

// A missing file is the normal "nothing written yet" case for callers, so it
// must not wait out the retry window.
func TestOpenMissingFileFailsImmediately(t *testing.T) {
	start := time.Now()
	_, err := Open(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open of a missing file: error = %v, want ErrNotExist", err)
	}
	if elapsed := time.Since(start); elapsed > transientFor/4 {
		t.Fatalf("Open of a missing file took %s; it must not retry", elapsed)
	}
}
