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

//go:build windows

package main

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func isManaged(exe string) bool {
	const packedUpgradeCode = `F0742CB3EE450F7479C37A9886B49FE5`
	const registryPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Installer\UpgradeCodes\` + packedUpgradeCode

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, registryPath, registry.QUERY_VALUE)
	if err == nil {
		// If we can open the key, check if our executable is listed as an installed product under this upgrade code
		// this means dbc is installed via MSI.
		defer k.Close()

		// so check if we're running from the location where dbc.msi installs to
		return strings.Contains(exe, `AppData\Roaming\Columnar\dbc`)
	}

	if strings.HasSuffix(filepath.Dir(exe), "\\Scripts") {
		// likely a pip install
		return true
	}

	return false
}
