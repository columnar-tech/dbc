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
	"path/filepath"

	"github.com/columnar-tech/dbc/config"
)

// VerifyPackageSignature verifies a legacy package's library signature while
// the extracted package is still in its private staging directory. Packages
// without a library file do not use library signatures.
func VerifyPackageSignature(stagingDir string, manifest config.Manifest) error {
	if manifest.Files.Driver == "" {
		return nil
	}

	library, err := os.Open(filepath.Join(stagingDir, manifest.Files.Driver))
	if err != nil {
		return fmt.Errorf("could not open driver file: %w", err)
	}
	defer library.Close()

	signatureName := manifest.Files.Signature
	if signatureName == "" {
		signatureName = manifest.Files.Driver + ".sig"
	}
	signature, err := os.Open(filepath.Join(stagingDir, signatureName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("signature file '%s' for driver is missing", signatureName)
		}
		return fmt.Errorf("failed to open signature file: %w", err)
	}
	defer signature.Close()

	if err := SignedByColumnar(library, signature); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}
