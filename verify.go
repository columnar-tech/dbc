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

// VerifyManifestSignature checks the driver library that m installed for this
// platform against its detached signature (m.Files.Signature, by default
// "<driver>.sig" next to it) using Columnar's signing key. A manifest that
// names no driver file has nothing to verify.
func VerifyManifestSignature(m config.Manifest) error {
	if m.Files.Driver == "" {
		return nil
	}

	path := filepath.Dir(m.Driver.Shared.Get(config.PlatformTuple()))

	lib, err := os.Open(filepath.Join(path, m.Files.Driver))
	if err != nil {
		return fmt.Errorf("could not open driver file: %w", err)
	}
	defer lib.Close()

	sigFile := m.Files.Signature
	if sigFile == "" {
		sigFile = m.Files.Driver + ".sig"
	}

	sig, err := os.Open(filepath.Join(path, sigFile))
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
