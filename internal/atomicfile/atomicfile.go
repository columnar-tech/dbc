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

// Package atomicfile writes a complete file before replacing its destination.
package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteFile writes data to a temporary file beside path, then atomically
// replaces path. Preparation or replacement failures leave the existing
// destination untouched. Directory sync is best effort and its errors are
// ignored after replacement.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return Write(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// Write calls write with a temporary file and replaces path after the callback,
// file sync, and close all succeed. Directory sync is best effort and its
// errors are ignored after replacement.
func Write(path string, perm os.FileMode, write func(io.Writer) error) (err error) {
	return writeFile(path, perm, write, replace, syncDirectory)
}

func writeFile(path string, perm os.FileMode, write func(io.Writer) error, replaceFile func(string, string) error, syncDir func(string) error) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("atomicfile: create temporary file for %s: %w", path, err)
	}
	tmp := f.Name()
	replaced := false
	defer func() {
		if removeErr := os.Remove(tmp); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil && !replaced {
			err = fmt.Errorf("atomicfile: remove temporary file %s: %w", tmp, removeErr)
		}
	}()

	if err = f.Chmod(perm); err != nil {
		f.Close()
		return fmt.Errorf("atomicfile: chmod temporary file for %s: %w", path, err)
	}
	if err = write(f); err != nil {
		f.Close()
		return fmt.Errorf("atomicfile: write temporary file for %s: %w", path, err)
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("atomicfile: sync temporary file for %s: %w", path, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("atomicfile: close temporary file for %s: %w", path, err)
	}
	if err = replaceFile(tmp, path); err != nil {
		return fmt.Errorf("atomicfile: replace %s: %w", path, err)
	}
	replaced = true
	_ = syncDir(dir)
	return nil
}
