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
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.toml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("destination = %q, want %q", got, "new")
	}
}

func TestWriteFailurePreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.toml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("write failed")
	err := Write(path, 0o600, func(w io.Writer) error {
		if _, err := w.Write([]byte("partial")); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Write error = %v, want wrapped %v", err, wantErr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("destination = %q, want old content", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("temporary file was not cleaned up: %v", entries)
	}
}

func TestReplaceFailurePreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.toml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("replace failed")
	replaceCalled := false
	err := writeFile(path, 0o600, func(w io.Writer) error {
		_, err := w.Write([]byte("new"))
		return err
	}, func(source, destination string) error {
		replaceCalled = true
		if destination != path {
			t.Fatalf("replace destination = %q, want %q", destination, path)
		}
		if _, err := os.Stat(source); err != nil {
			t.Fatalf("temporary source is not ready for replacement: %v", err)
		}
		return wantErr
	}, syncDirectory)
	if !replaceCalled {
		t.Fatal("replace function was not called")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("writeFile error = %v, want wrapped %v", err, wantErr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("destination = %q, want old content", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("temporary file was not cleaned up: %v", entries)
	}
}

func TestDirectorySyncFailureDoesNotReportReplacementFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.toml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	syncErr := errors.New("directory sync failed")
	err := writeFile(path, 0o600, func(w io.Writer) error {
		_, err := w.Write([]byte("new"))
		return err
	}, replace, func(string) error { return syncErr })
	if err != nil {
		t.Fatalf("writeFile returned post-replacement sync error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("destination = %q, want new content", got)
	}
}
