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
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

type packageCleanupOperations struct {
	remove    func(string) error
	removeAll func(string) error
}

func cleanupInstalledPackage(cfg Config, root string, info DriverInfo, remove func(string) error, removeAll func(string) error) error {
	return cleanupInstalledPackageWithOperations(cfg, root, info, packageCleanupOperations{remove: remove, removeAll: removeAll})
}

func cleanupInstalledPackageWithOperations(cfg Config, root string, info DriverInfo, operations packageCleanupOperations) error {
	if info.Source != "dbc" || root == "" {
		return nil
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	scope, err := packageRegistrationScopeForConfig(cfg)
	if err != nil {
		return nil
	}
	entries, err := readDirectoryEntries(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read package installation root %s: %w", root, err)
	}

	matched := make([]string, 0, 1)
	for _, entry := range entries {
		generation := filepath.Join(root, entry.Name())
		receipt, ok := readPackageInstallReceipt(root, generation)
		if !ok || !sameRuntimeID(receipt.RuntimeID, info.ID) || receipt.Platform != PlatformTuple() || receipt.RegistrationScope != scope {
			continue
		}
		if receiptMatchesRegistration(cfg, root, generation, receipt, info) {
			matched = append(matched, generation)
		}
	}

	if len(matched) == 0 {
		if hasTransactionEvidence(entries, info.ID) {
			return nil
		}
		if generation := legacyPackageGeneration(root, info); generation != "" {
			return operations.removeAll(generation)
		}
		if generation := legacyMetadataSidecar(root, info); generation != "" {
			return operations.removeAll(generation)
		}
		return nil
	}

	var strictErr error
	for _, generation := range matched {
		strictErr = errors.Join(strictErr, removePackageGeneration(generation, operations.remove, operations.removeAll))
	}
	if strictErr != nil {
		return strictErr
	}
	cleanupStalePackageGenerations(cfg, root, info, matched, operations.remove, operations.removeAll)
	return nil
}

func receiptMatchesRegistration(cfg Config, root, generation string, receipt packageInstallReceipt, info DriverInfo) bool {
	shared := info.Driver.Shared.Get(receipt.Platform)
	if shared == "" || hasParentTraversal(shared) {
		return false
	}
	identity := shared
	if receipt.LibraryKind == packageLibraryFile {
		identity = receipt.OwnedLibraryFilename
		expected := filepath.Join(generation, receipt.OwnedLibraryFilename)
		if !sameResolvedFilesystemPath(resolvePackagePath(root, shared), expected) {
			return false
		}
	}
	fingerprint, err := runtimeRegistrationFingerprint(cfg, receipt.RuntimeID, receipt.Platform, info, receipt.LibraryKind, identity)
	return err == nil && fingerprint == receipt.RegistrationFingerprintValue
}

func cleanupStalePackageGenerations(cfg Config, root string, current DriverInfo, alreadyRemoved []string, remove func(string) error, removeAll func(string) error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return
	}
	scope, err := packageRegistrationScopeForConfig(cfg)
	if err != nil {
		return
	}
	entries, err := readDirectoryEntries(root)
	if err != nil {
		return
	}
	referenced, uncertainReferences := referencedPaths(root, current.Driver.Shared)
	if uncertainReferences {
		return
	}
	for _, entry := range entries {
		generation := filepath.Join(root, entry.Name())
		if containsFilesystemPathOrUncertain(alreadyRemoved, generation) || generationReferenced(generation, referenced) {
			continue
		}
		receipt, ok := readPackageInstallReceipt(root, generation)
		if !ok || !sameRuntimeID(receipt.RuntimeID, current.ID) || receipt.RegistrationScope != scope {
			continue
		}
		_ = removePackageGeneration(generation, remove, removeAll)
	}
}

func readDirectoryEntries(path string) (entries []fs.DirEntry, err error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if readErr != nil {
		readErr = fmt.Errorf("read directory entries from %s: %w", path, readErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close directory %s: %w", path, closeErr)
	}
	return entries, errors.Join(readErr, closeErr)
}

func removePackageGeneration(generation string, remove func(string) error, removeAll func(string) error) error {
	entries, err := readDirectoryEntries(generation)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read package generation %s: %w", generation, err)
	}
	var payloadErr error
	for _, entry := range entries {
		if entry.Name() == packageInstallReceiptFilename {
			continue
		}
		path := filepath.Join(generation, entry.Name())
		info, statErr := os.Lstat(path)
		if statErr != nil {
			payloadErr = errors.Join(payloadErr, fmt.Errorf("inspect package generation entry %s: %w", path, statErr))
			continue
		}
		if info.IsDir() {
			payloadErr = errors.Join(payloadErr, removeAll(path))
		} else {
			payloadErr = errors.Join(payloadErr, remove(path))
		}
	}
	if payloadErr != nil {
		return fmt.Errorf("remove package generation payload %s: %w", generation, payloadErr)
	}
	if err := remove(filepath.Join(generation, packageInstallReceiptFilename)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove package generation receipt %s: %w", generation, err)
	}
	if err := os.Remove(generation); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove package generation directory %s: %w", generation, err)
	}
	return nil
}

func hasTransactionEvidence(entries []fs.DirEntry, runtimeID string) bool {
	prefix := ".dbc-package-" + runtimeID + "-"
	for _, entry := range entries {
		name := entry.Name()
		if runtime.GOOS == "windows" {
			if len(name) >= len(prefix) && strings.EqualFold(name[:len(prefix)], prefix) {
				return true
			}
		} else if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func legacyPackageGeneration(root string, info DriverInfo) string {
	if info.Source != "dbc" || validatePackageFilename(info.ID) != nil || info.Version == nil {
		return ""
	}
	shared := info.Driver.Shared.Get(PlatformTuple())
	if shared == "" || hasParentTraversal(shared) {
		return ""
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	sharedAbs := resolvePackagePath(rootAbs, shared)
	generation := filepath.Dir(sharedAbs)
	if filepath.Dir(generation) != filepath.Clean(rootAbs) || filepath.Dir(sharedAbs) != generation {
		return ""
	}
	if !legacyGenerationNameMatches(filepath.Base(generation), info.ID, PlatformTuple(), info.Version) {
		return ""
	}
	generationInfo, err := os.Lstat(generation)
	if err != nil || !generationInfo.IsDir() || generationInfo.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	if _, err := os.Lstat(filepath.Join(generation, packageInstallReceiptFilename)); err == nil {
		return ""
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	sharedInfo, err := os.Lstat(sharedAbs)
	if err != nil || !sharedInfo.Mode().IsRegular() {
		return ""
	}
	return generation
}

func legacyMetadataSidecar(root string, info DriverInfo) string {
	if info.Source != "dbc" || validatePackageFilename(info.ID) != nil || info.Version == nil {
		return ""
	}
	versionString := info.Version.String()
	parsedVersion, err := semver.NewVersion(versionString)
	if err != nil || parsedVersion.String() != versionString {
		return ""
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	generation := filepath.Join(rootAbs, info.ID+"_"+PlatformTuple()+"_v"+info.Version.String())
	if filepath.Dir(generation) != rootAbs {
		return ""
	}
	generationInfo, err := os.Lstat(generation)
	if err != nil || !generationInfo.IsDir() || generationInfo.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	if _, err := os.Lstat(filepath.Join(generation, packageInstallReceiptFilename)); err == nil {
		return ""
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	references, uncertain := referencedPaths(rootAbs, info.Driver.Shared)
	if uncertain {
		return ""
	}
	for _, reference := range references {
		if pathWithin(generation, reference) {
			return ""
		}
	}
	return generation
}

func legacyGenerationNameMatches(name, runtimeID, platform string, registeredVersion *semver.Version) bool {
	if registeredVersion == nil {
		return false
	}
	prefix := runtimeID + "_" + platform + "_v"
	if !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
		return false
	}
	version, err := semver.NewVersion(strings.TrimPrefix(name, prefix))
	return err == nil && version.String() == registeredVersion.String()
}

func referencedPaths(root string, shared driverMap) ([]string, bool) {
	var result []string
	for path := range shared.Paths() {
		if path != "" {
			if hasParentTraversal(path) {
				return nil, true
			}
			result = append(result, resolvePackagePath(root, path))
		}
	}
	return result, false
}

func hasParentTraversal(path string) bool {
	path = strings.ReplaceAll(path, `\`, "/")
	for _, component := range strings.Split(path, "/") {
		if component == ".." {
			return true
		}
	}
	return false
}

func resolvePackagePath(root, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	resolved, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return resolved
}

func generationReferenced(generation string, references []string) bool {
	for _, reference := range references {
		if pathWithin(generation, reference) {
			return true
		}
	}
	return false
}

func pathWithin(parent, child string) bool {
	parent, parentCertain := canonicalFilesystemPath(parent)
	child, childCertain := canonicalFilesystemPath(child)
	if !parentCertain || !childCertain {
		return true
	}
	if runtime.GOOS == "windows" {
		parent = strings.ToLower(parent)
		child = strings.ToLower(child)
	}
	if sameFilesystemPathOrUncertain(parent, child) {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func containsFilesystemPathOrUncertain(paths []string, target string) bool {
	for _, path := range paths {
		if sameFilesystemPathOrUncertain(path, target) {
			return true
		}
	}
	return false
}

func sameRuntimeID(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func sameFilesystemPathOrUncertain(a, b string) bool {
	equal, certain := sameResolvedFilesystemPathWithCertainty(a, b)
	return equal || !certain
}

func sameResolvedFilesystemPath(a, b string) bool {
	equal, certain := sameResolvedFilesystemPathWithCertainty(a, b)
	return equal && certain
}

func sameResolvedFilesystemPathWithCertainty(a, b string) (bool, bool) {
	resolvedA, certainA := canonicalFilesystemPath(a)
	resolvedB, certainB := canonicalFilesystemPath(b)
	if !certainA || !certainB {
		return false, false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(resolvedA, resolvedB), true
	}
	return resolvedA == resolvedB, true
}

func canonicalFilesystemPath(path string) (string, bool) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", false
	}
	current := absolute
	var suffix []string
	for {
		info, statErr := os.Lstat(current)
		if statErr == nil {
			if len(suffix) > 0 && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return "", false
			}
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", false
			}
			if len(suffix) > 0 {
				targetInfo, targetErr := os.Stat(current)
				if targetErr != nil || !targetInfo.IsDir() {
					return "", false
				}
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), true
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			return "", false
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
