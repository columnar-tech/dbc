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
	"io/fs"
	"os"

	"github.com/columnar-tech/dbc/config"
)

// VerifyPackageSignature checks the driver library of a package extracted to
// stagingDir against its detached signature (m.Files.Signature, by default
// "<driver>.sig") using Columnar's signing key. It has the shape of a
// config.PackageVerifier. Both files are opened through an os.Root, so a
// manifest path that leaves stagingDir (via "..", an absolute path, or a
// symlink) is an error rather than verifying a file elsewhere.
func VerifyPackageSignature(stagingDir string, m config.Manifest) error {
	if m.Files.Driver == "" {
		return fmt.Errorf("package manifest does not specify Files.driver")
	}

	root, err := os.OpenRoot(stagingDir)
	if err != nil {
		return fmt.Errorf("could not open staging directory: %w", err)
	}
	defer root.Close()

	lib, err := root.Open(m.Files.Driver)
	if err != nil {
		return fmt.Errorf("could not open driver file: %w", err)
	}
	defer lib.Close()

	sigFile := m.Files.Signature
	if sigFile == "" {
		sigFile = m.Files.Driver + ".sig"
	}

	sig, err := root.Open(sigFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("signature file '%s' for driver is missing", sigFile)
		}
		return fmt.Errorf("failed to open signature file: %w", err)
	}
	defer sig.Close()

	if err := SignedByColumnar(lib, sig); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}
