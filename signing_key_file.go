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
}

// newSigningKeyFile resolves name, as written in public_key_file, against the
// directory of definedIn, the configuration file that defines it. The paths
// are not resolved through symbolic links, and neither "~" nor environment
// variables are expanded.
func newSigningKeyFile(name, definedIn string) (*signingKeyFile, error) {
	if name == "" {
		return nil, errors.New("public_key_file must not be empty")
	}
	if !keyFilesSupported {
		return nil, errors.New("public_key_file is not supported in the browser; use public_key")
	}
	if !filepath.IsAbs(name) {
		if definedIn == "" || !filepath.IsAbs(definedIn) {
			return nil, fmt.Errorf("public_key_file %q is relative, but the configuration was not read from a file it could be relative to", name)
		}
		name = filepath.Join(filepath.Dir(definedIn), name)
	}
	return &signingKeyFile{path: filepath.Clean(name)}, nil
}

func (f *signingKeyFile) load() (*SigningKey, error) {
	data, err := readSigningKeyFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("public_key_file %s: %w", f.path, err)
	}
	key, err := NewSigningKey(data)
	if err != nil {
		return nil, fmt.Errorf("public_key_file %s: %w", f.path, err)
	}
	return key, nil
}

// readSigningKeyFile reads a regular file of at most maxSigningKeySize bytes.
// The file is checked before it is opened, so that a FIFO or device is not
// opened at all, and again after, so that it was not replaced in between.
func readSigningKeyFile(path string) ([]byte, error) {
	before, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if before.Size() > maxSigningKeySize {
		return nil, fmt.Errorf("larger than %d bytes", maxSigningKeySize)
	}

	f, err := os.Open(path)
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
