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
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/columnar-tech/dbc/internal/hostpath"
)

// cleanupLegacyPackageRegistration removes only the exact registered library
// from a receipt-less direct child directory. The directory itself is removed
// only when that exact file was its last entry.
func cleanupLegacyPackageRegistration(root string, info DriverInfo, references []string, referencesCertain bool, operations packageCleanupOperations) error {
	if info.Source != "dbc" || root == "" || !referencesCertain || !supportsPinnedCleanup() {
		return nil
	}
	root, err := hostpath.Abs(root)
	if err != nil {
		return nil
	}
	parent, err := os.OpenRoot(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open legacy package root %s: %w", root, err)
	}
	defer parent.Close()
	return cleanupLegacyPackageRegistrationAtRoot(parent, root, info, references, referencesCertain, operations)
}

func cleanupLegacyPackageRegistrationAtRoot(parent *os.Root, root string, info DriverInfo, references []string, referencesCertain bool, operations packageCleanupOperations) error {
	if info.Source != "dbc" || validatePackageFilename(info.ID) != nil || !referencesCertain {
		return nil
	}
	shared := info.Driver.Shared.Get(PlatformTuple())
	if shared == "" || hasParentTraversal(shared) {
		return nil
	}
	rootAbs, err := hostpath.Abs(root)
	if err != nil {
		return nil
	}
	sharedAbs := resolvePackagePath(rootAbs, shared)
	generationPath := hostpath.Dir(sharedAbs)
	if !sameResolvedFilesystemPath(hostpath.Dir(generationPath), hostpath.Clean(rootAbs)) || hostpath.Dir(sharedAbs) != generationPath {
		return nil
	}
	generationName := hostpath.Base(generationPath)
	sharedName := hostpath.Base(sharedAbs)
	if generationName == "." || generationName == ".." || sharedName == "." || sharedName == ".." || sharedName == packageInstallReceiptFilename {
		return nil
	}
	if hasReservedTransactionAncestor(generationPath) {
		return nil
	}
	for _, reference := range references {
		if sameFilesystemPathOrUncertain(sharedAbs, reference) {
			return nil
		}
	}

	generation, ok := openVerifiedGeneration(parent, generationName, rootAbs, operations)
	if !ok {
		return nil
	}
	defer generation.Close()

	if _, err := generation.Lstat(packageInstallReceiptFilename); err == nil || !errors.Is(err, fs.ErrNotExist) {
		// Any receipt, including a corrupt one, reserves this directory for the
		// transaction cleanup path. A missing receipt is required for fallback.
		return nil
	}
	currentGenerationInfo, err := generation.Stat(".")
	if err != nil || legacyMetadataSidecarElsewhere(parent, generationName, currentGenerationInfo, info) {
		return nil
	}
	payloadInfo, err := generation.Lstat(sharedName)
	if err != nil || !payloadInfo.Mode().IsRegular() {
		return nil
	}
	if operations.beforeRemove != nil {
		if err := operations.beforeRemove(sharedAbs); err != nil {
			return fmt.Errorf("remove legacy package payload %s: %w", sharedAbs, err)
		}
	}
	currentPayload, err := generation.Lstat(sharedName)
	if err != nil || !currentPayload.Mode().IsRegular() || !os.SameFile(payloadInfo, currentPayload) {
		return nil
	}
	if err := generation.Remove(sharedName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove legacy package payload %s: %w", sharedAbs, err)
	}

	remaining, err := readRootDirectoryEntries(generation)
	if err != nil {
		return fmt.Errorf("inspect legacy package directory %s after payload removal: %w", generationPath, err)
	}
	if len(remaining) != 0 {
		return nil
	}
	generationInfo, err := generation.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect legacy package directory %s before removal: %w", generationPath, err)
	}
	if operations.beforeGenerationRemove != nil {
		operations.beforeGenerationRemove(generationPath)
	}
	if err := generation.Close(); err != nil {
		return fmt.Errorf("close legacy package directory %s before removal: %w", generationPath, err)
	}
	currentGeneration, err := parent.Lstat(generationName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify legacy package directory %s before removal: %w", generationPath, err)
	}
	if !currentGeneration.IsDir() || currentGeneration.Mode()&os.ModeSymlink != 0 || !os.SameFile(generationInfo, currentGeneration) {
		return nil
	}
	if err := parent.Remove(generationName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove empty legacy package directory %s: %w", generationPath, err)
	}
	return nil
}

// legacyMetadataSidecarElsewhere protects a registered external path when a
// separate standard legacy generation for the same registration exists. The
// older installer named package directories from the archive basename, so this
// is only a conservative guard; an extra standard generation can retain an
// arbitrary-basename payload until a later cleanup attempt.
func legacyMetadataSidecarElsewhere(parent *os.Root, generationName string, generationInfo os.FileInfo, info DriverInfo) bool {
	if info.Version == nil {
		return false
	}
	knownName := info.ID + "_" + PlatformTuple() + "_v" + info.Version.String()
	if knownName == generationName {
		return false
	}
	knownInfo, err := parent.Lstat(knownName)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	if !knownInfo.IsDir() || knownInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return !os.SameFile(generationInfo, knownInfo)
}
