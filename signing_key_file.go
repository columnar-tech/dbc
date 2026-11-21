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

package dbc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// signingKeyFile is a public_key_file of a [signing_keys] definition. The file
// is read only when a registry that names the definition needs its key, so a
// missing file only fails the operations that use it. It is read again on
// every use, so a long-lived client sees an updated certificate; an
// operation that must verify all of its packages with one certificate keeps
// the key it got first.
type signingKeyFile struct {
	// path is the absolute path of the file.
	path string
	// root is set for a file of a project's dbc.toml: the directory of
	// dbc.toml, which the file is read through so that it cannot be outside
	// that directory, also through a symbolic link. rel is the path of the
	// file relative to root.
	root, rel string
}

// newSigningKeyFile resolves name, as written in public_key_file, against the
// directory of definedIn, the configuration file that defines it. Neither "~"
// nor environment variables are expanded.
//
// For a project's dbc.toml (project is true), name must be a local relative
// path: an absolute path, such as a UNC path on Windows, or a path that
// leaves the directory of dbc.toml with "..", is rejected before any file is
// accessed, because a cloned repository should not make dbc read a file
// elsewhere, for example from a network share. Symbolic links in the user
// config are followed.
func newSigningKeyFile(name, definedIn string, project bool) (*signingKeyFile, error) {
	if name == "" {
		return nil, errors.New("public_key_file must not be empty")
	}
	if !keyFilesSupported {
		return nil, errors.New("public_key_file is not supported in the browser; use public_key")
	}
	if project && !filepath.IsLocal(name) {
		return nil, fmt.Errorf("public_key_file %q must be a path inside the directory of dbc.toml, relative to it", name)
	}
	if !filepath.IsAbs(name) {
		// On Windows, "\keys\a.asc" and "C:keys\a.asc" are neither absolute
		// nor relative to a directory: they depend on the current drive or
		// on the current directory of a drive.
		if filepath.VolumeName(name) != "" || (name != "" && os.IsPathSeparator(name[0])) {
			return nil, fmt.Errorf("public_key_file %q must be an absolute path with a drive or a path relative to the configuration file", name)
		}
		if definedIn == "" || !filepath.IsAbs(definedIn) {
			return nil, fmt.Errorf("public_key_file %q is relative, but the configuration was not read from a file it could be relative to", name)
		}
		dir := filepath.Dir(definedIn)
		f := &signingKeyFile{path: filepath.Join(dir, name)}
		if project {
			f.root, f.rel = dir, filepath.Clean(name)
		}
		return f, nil
	}
	return &signingKeyFile{path: filepath.Clean(name)}, nil
}

func (f *signingKeyFile) load() (*SigningKey, error) {
	var data []byte
	var err error
	if f.root != "" {
		data, err = readSigningKeyFileIn(f.root, f.rel)
	} else {
		data, err = readSigningKeyFile(f.path)
	}
	if err != nil {
		return nil, fmt.Errorf("public_key_file %s: %w", f.path, err)
	}
	key, err := NewSigningKey(data)
	if err != nil {
		return nil, fmt.Errorf("public_key_file %s: %w", f.path, err)
	}
	return key, nil
}

// readSigningKeyFileIn reads the file rel inside the directory root, as
// readSigningKeyFile does. Neither rel nor a symbolic link on the way can
// lead outside root.
func readSigningKeyFileIn(root, rel string) ([]byte, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return readRegularFile(func() (os.FileInfo, error) { return r.Stat(rel) }, func() (*os.File, error) { return r.Open(rel) })
}

// readSigningKeyFile reads a regular file of at most maxSigningKeySize bytes.
func readSigningKeyFile(path string) ([]byte, error) {
	return readRegularFile(func() (os.FileInfo, error) { return os.Stat(path) }, func() (*os.File, error) { return os.Open(path) })
}

// readRegularFile reads a regular file of at most maxSigningKeySize bytes.
// The file is checked with stat before it is opened with open, so that a FIFO
// or device is not opened at all, and again after, so that it was not
// replaced in between.
func readRegularFile(stat func() (os.FileInfo, error), open func() (*os.File, error)) ([]byte, error) {
	before, err := stat()
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if before.Size() > maxSigningKeySize {
		return nil, fmt.Errorf("larger than %d bytes", maxSigningKeySize)
	}

	f, err := open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, errors.New("the file changed while it was opened")
	}

	data, err := io.ReadAll(io.LimitReader(f, maxSigningKeySize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSigningKeySize {
		return nil, fmt.Errorf("larger than %d bytes", maxSigningKeySize)
	}
	return data, nil
}
