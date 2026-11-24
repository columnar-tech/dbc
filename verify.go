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
	"github.com/columnar-tech/dbc/config"
)

// VerifyPackageSignature checks the driver library of a package extracted to
// stagingDir against its detached signature (m.Files.Signature, by default
// "<driver>.sig") using Columnar's signing key. It has the shape of a
// config.PackageVerifier. Both files are opened through an os.Root, so a
// manifest path that leaves stagingDir (via "..", an absolute path, or a
// symlink) is an error rather than verifying a file elsewhere.
func VerifyPackageSignature(stagingDir string, m config.Manifest) error {
	key, err := ColumnarSigningKey()
	if err != nil {
		return err
	}
	return key.VerifyPackage(stagingDir, m)
}
