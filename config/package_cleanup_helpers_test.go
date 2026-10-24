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

package config

import (
	"os"

	"github.com/columnar-tech/dbc/internal/hostpath"
)

// readPackageInstallReceipt accepts evidence only when its identity is
// structurally consistent with a direct generation under the absolute primary
// root. Invalid or absent evidence is treated as no ownership proof.
func readPackageInstallReceipt(primaryRoot, generationDir string) (packageInstallReceipt, bool) {
	var empty packageInstallReceipt
	root, err := hostpath.Abs(primaryRoot)
	if err != nil {
		return empty, false
	}
	generation, err := hostpath.Abs(generationDir)
	if err != nil {
		return empty, false
	}
	root = hostpath.Clean(root)
	generation = hostpath.Clean(generation)
	if hostpath.Dir(generation) != root {
		return empty, false
	}
	parent, err := os.OpenRoot(root)
	if err != nil {
		return empty, false
	}
	defer parent.Close()
	generationName := hostpath.Base(generation)
	generationInfo, err := parent.Lstat(generationName)
	if err != nil || !generationInfo.IsDir() || generationInfo.Mode()&os.ModeSymlink != 0 {
		return empty, false
	}
	generationRoot, err := parent.OpenRoot(generationName)
	if err != nil {
		return empty, false
	}
	defer generationRoot.Close()
	openedGenerationInfo, err := generationRoot.Stat(".")
	if err != nil || !os.SameFile(generationInfo, openedGenerationInfo) {
		return empty, false
	}
	return readPackageInstallReceiptAtRoot(generationRoot, generationName)
}

func cleanupInstalledPackage(cfg Config, root string, info DriverInfo) error {
	return cleanupInstalledPackageWithOperations(cfg, root, info, packageCleanupOperations{})
}

func cleanupInstalledPackageWithOperations(cfg Config, root string, info DriverInfo, operations packageCleanupOperations) error {
	return cleanupInstalledPackageWithReferences(cfg, root, info, nil, true, operations)
}

func cleanupStalePackageGenerations(cfg Config, root string, current DriverInfo, alreadyRemoved []string) {
	referenced, uncertain := referencedPaths(root, current.Driver.Shared)
	cleanupStalePackageGenerationsWithReferences(cfg, root, current, alreadyRemoved, referenced, uncertain, packageCleanupOperations{})
}
