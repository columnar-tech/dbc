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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// WriteFilePreservingMode atomically replaces path while retaining the
// permission bits of an existing regular file. For a symlink to a regular
// file, it replaces the symlink itself with a regular file using the target's
// permission bits; the target is left unchanged. Dangling symlinks are treated
// as new destinations. Other nonregular destinations are rejected before any
// replacement is attempted. Existing destinations use a 0600 temporary file
// before its mode is changed to the saved permission bits. New destinations
// use perm when creating the temporary file so the process umask applies.
func WriteFilePreservingMode(path string, data []byte, perm os.FileMode) error {
	existing, existingPerm, err := destinationMode(path)
	if err != nil {
		return err
	}
	createPerm, finalPerm, chmod := preservingTempPermissions(existing, existingPerm, perm)
	return writeFileWithTemp(path, finalPerm, chmod, func(dir, base string) (*os.File, error) {
		return createUniqueTemp(dir, strings.TrimSuffix(base, "*"), createPerm)
	}, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	}, replace, syncDirectory)
}

func destinationMode(path string) (bool, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("atomicfile: inspect destination %s: %w", path, err)
	}
	if info.Mode().IsRegular() {
		return true, info.Mode().Perm(), nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, 0, nil
		}
		if err != nil {
			return false, 0, fmt.Errorf("atomicfile: inspect symlink target %s: %w", path, err)
		}
		if target.Mode().IsRegular() {
			return true, target.Mode().Perm(), nil
		}
	}
	return false, 0, fmt.Errorf("atomicfile: destination %s is not a regular file", path)
}

func preservingTempPermissions(existing bool, existingPerm, newPerm os.FileMode) (createPerm, finalPerm os.FileMode, chmod bool) {
	if existing {
		return 0o600, existingPerm, true
	}
	return newPerm, newPerm, false
}

// Write calls write with a temporary file and replaces path after the callback,
// file sync, and close all succeed. Directory sync is best effort and its
// errors are ignored after replacement.
func Write(path string, perm os.FileMode, write func(io.Writer) error) (err error) {
	return writeFile(path, perm, write, replace, syncDirectory)
}

func writeFile(path string, perm os.FileMode, write func(io.Writer) error, replaceFile func(string, string) error, syncDir func(string) error) (err error) {
	return writeFileWithTemp(path, perm, true, func(dir, base string) (*os.File, error) {
		return os.CreateTemp(dir, base)
	}, write, replaceFile, syncDir)
}

func writeFileWithTemp(path string, perm os.FileMode, chmod bool, createTemp func(string, string) (*os.File, error), write func(io.Writer) error, replaceFile func(string, string) error, syncDir func(string) error) (err error) {
	dir := filepath.Dir(path)
	f, err := createTemp(dir, "."+filepath.Base(path)+".tmp-*")
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

	if chmod {
		if err = f.Chmod(perm); err != nil {
			f.Close()
			return fmt.Errorf("atomicfile: chmod temporary file for %s: %w", path, err)
		}
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

func createUniqueTemp(dir, prefix string, perm os.FileMode) (*os.File, error) {
	for range 100 {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, fmt.Errorf("atomicfile: generate temporary filename: %w", err)
		}
		path := filepath.Join(dir, prefix+hex.EncodeToString(random[:]))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	return nil, fmt.Errorf("atomicfile: could not allocate a unique temporary file in %s", dir)
}
