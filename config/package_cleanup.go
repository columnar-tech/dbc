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
	"sort"
	"strings"

	"github.com/columnar-tech/dbc/internal/hostpath"
)

type packageCleanupOperations struct {
	beforeRemove             func(string) error
	beforeRemoveAll          func(string) error
	removeRegistration       func(Config, DriverInfo) error
	hashOwnedLibrary         ownedLibraryHashFunc
	statGeneration           func(*os.Root) (os.FileInfo, error)
	beforeGenerationOpen     func(string) error
	afterGenerationLstat     func(string)
	afterGenerationVerified  func(string)
	afterReceiptVerified     func(string)
	afterGenerationEnumerate func(string)
	beforeGenerationRemove   func(string)
}

type verifiedPackageGeneration struct {
	name     string
	root     *os.Root
	fileInfo os.FileInfo
}

func cleanupInstalledPackage(cfg Config, root string, info DriverInfo) error {
	return cleanupInstalledPackageWithOperations(cfg, root, info, packageCleanupOperations{})
}

// Cleanup runs after driver registration has been removed. If cleanup fails,
// package files may remain without a registration that can be uninstalled
// again. TODO: Define a safe recovery path for these retained generations.
func cleanupInstalledPackageWithOperations(cfg Config, root string, info DriverInfo, operations packageCleanupOperations) error {
	return cleanupInstalledPackageWithReferences(cfg, root, info, nil, true, operations)
}

func cleanupInstalledPackageWithReferences(cfg Config, root string, info DriverInfo, otherRegistrations []driverMap, referencesCertain bool, operations packageCleanupOperations) error {
	if info.Source != "dbc" || root == "" {
		return nil
	}
	if !referencesCertain {
		return nil
	}
	if !supportsPinnedCleanup() {
		return nil
	}
	root, err := hostpath.Abs(root)
	if err != nil {
		return nil
	}
	scope, err := packageRegistrationScopeForConfig(cfg)
	if err != nil {
		return nil
	}
	rootHandle, err := os.OpenRoot(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open package installation root %s: %w", root, err)
	}
	defer rootHandle.Close()
	entries, err := readRootDirectoryEntries(rootHandle)
	if err != nil {
		return fmt.Errorf("read package installation root %s: %w", root, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	referenced, uncertain := referencedPathsForRegistrations(root, otherRegistrations)
	if uncertain {
		return nil
	}

	matchedPaths := make([]string, 0, 1)
	matchedCount := 0
	var strictErr error
	for _, entry := range entries {
		generation := entry.Name()
		generationRoot, ok := openVerifiedGeneration(rootHandle, generation, root, operations)
		if !ok {
			continue
		}
		receipt, ok := readPackageInstallReceiptAtRoot(generationRoot, generation)
		if !ok || !sameRuntimeID(receipt.RuntimeID, info.ID) || receipt.Platform != PlatformTuple() || receipt.RegistrationScope != scope {
			_ = generationRoot.Close()
			continue
		}
		if operations.afterReceiptVerified != nil {
			operations.afterReceiptVerified(hostpath.Join(root, generation))
		}
		generationPath := hostpath.Join(root, generation)
		if receiptMatchesRegistration(cfg, root, generationPath, receipt, info) {
			matchedCount++
			matchedPaths = append(matchedPaths, generationPath)
			if !generationReferenced(generationPath, referenced) {
				_, integrityErr := verifyPackageOwnedLibraryIntegrityAtRoot(generationRoot, receipt, operations.hashOwnedLibrary)
				if integrityErr != nil {
					strictErr = errors.Join(strictErr, fmt.Errorf("verify owned package library in %s: %w", generationPath, integrityErr))
				} else {
					generationInfo, statErr := statPackageGenerationRoot(generationRoot, operations)
					if statErr != nil {
						strictErr = errors.Join(strictErr, fmt.Errorf("inspect package generation %s: %w", generationPath, statErr))
					} else {
						strictErr = errors.Join(strictErr, removePackageGenerationAtRoot(rootHandle, verifiedPackageGeneration{name: generation, root: generationRoot, fileInfo: generationInfo}, root, operations))
					}
				}
			}
		}
		_ = generationRoot.Close()
	}

	if matchedCount == 0 {
		if hasTransactionEvidence(entries, info.ID) {
			return nil
		}
		return cleanupLegacyPackageRegistrationAtRoot(rootHandle, root, info, referenced, true, operations)
	}
	if strictErr != nil {
		return strictErr
	}
	cleanupStalePackageGenerationsWithRoot(cfg, rootHandle, root, info, matchedPaths, referenced, false, operations)
	return nil
}

func statPackageGenerationRoot(generationRoot *os.Root, operations packageCleanupOperations) (os.FileInfo, error) {
	if operations.statGeneration != nil {
		return operations.statGeneration(generationRoot)
	}
	return generationRoot.Stat(".")
}

func legacyPackageReplacementCandidate(cfg Config, root, registrationLocation, runtimeID string) (DriverInfo, bool) {
	entries, err := readDirectoryEntries(root)
	if err != nil || hasTransactionEvidence(entries, runtimeID) {
		return DriverInfo{}, false
	}
	previous, ok := readPrimaryRuntimeRegistration(cfg, registrationLocation, runtimeID)
	if !ok || !sameRuntimeID(previous.ID, runtimeID) || previous.Source != "dbc" {
		return DriverInfo{}, false
	}
	return previous, true
}

func receiptMatchesRegistration(cfg Config, root, generation string, receipt packageInstallReceipt, info DriverInfo) bool {
	shared := info.Driver.Shared.Get(receipt.Platform)
	if shared == "" || hasParentTraversal(shared) {
		return false
	}
	identity := shared
	if receipt.LibraryKind == packageLibraryFile {
		identity = receipt.OwnedLibraryFilename
		expected := hostpath.Join(generation, receipt.OwnedLibraryFilename)
		if !sameResolvedFilesystemPath(resolvePackagePath(root, shared), expected) {
			return false
		}
	}
	fingerprint, err := runtimeRegistrationFingerprint(cfg, receipt.RuntimeID, receipt.Platform, info, receipt.LibraryKind, identity)
	return err == nil && fingerprint == receipt.RegistrationFingerprintValue
}

func cleanupStalePackageGenerations(cfg Config, root string, current DriverInfo, alreadyRemoved []string) {
	referenced, uncertain := referencedPaths(root, current.Driver.Shared)
	cleanupStalePackageGenerationsWithReferences(cfg, root, current, alreadyRemoved, referenced, uncertain, packageCleanupOperations{})
}

func cleanupStalePackageGenerationsWithReferences(cfg Config, root string, current DriverInfo, alreadyRemoved []string, referenced []string, uncertainReferences bool, operations packageCleanupOperations) {
	root, err := hostpath.Abs(root)
	if err != nil {
		return
	}
	if !supportsPinnedCleanup() {
		return
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer rootHandle.Close()
	cleanupStalePackageGenerationsWithRoot(cfg, rootHandle, root, current, alreadyRemoved, referenced, uncertainReferences, operations)
}

func cleanupStalePackageGenerationsWithRoot(cfg Config, rootHandle *os.Root, root string, current DriverInfo, alreadyRemoved []string, referenced []string, uncertainReferences bool, operations packageCleanupOperations) {
	root, err := hostpath.Abs(root)
	if err != nil {
		return
	}
	scope, err := packageRegistrationScopeForConfig(cfg)
	if err != nil {
		return
	}
	entries, err := readRootDirectoryEntries(rootHandle)
	if err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if uncertainReferences {
		return
	}
	for _, entry := range entries {
		generation := hostpath.Join(root, entry.Name())
		if containsFilesystemPathOrUncertain(alreadyRemoved, generation) || generationReferenced(generation, referenced) {
			continue
		}
		generationRoot, ok := openVerifiedGeneration(rootHandle, entry.Name(), root, operations)
		if !ok {
			continue
		}
		receipt, ok := readPackageInstallReceiptAtRoot(generationRoot, entry.Name())
		if !ok || !sameRuntimeID(receipt.RuntimeID, current.ID) || receipt.RegistrationScope != scope {
			_ = generationRoot.Close()
			continue
		}
		if operations.afterReceiptVerified != nil {
			operations.afterReceiptVerified(generation)
		}
		_, integrityErr := verifyPackageOwnedLibraryIntegrityAtRoot(generationRoot, receipt, operations.hashOwnedLibrary)
		if integrityErr != nil {
			_ = generationRoot.Close()
			continue
		}
		generationInfo, statErr := generationRoot.Stat(".")
		if statErr != nil {
			_ = generationRoot.Close()
			continue
		}
		_ = removePackageGenerationAtRoot(rootHandle, verifiedPackageGeneration{name: entry.Name(), root: generationRoot, fileInfo: generationInfo}, root, operations)
		_ = generationRoot.Close()
	}
}

func referencedPathsForRegistrations(root string, registrations []driverMap) ([]string, bool) {
	var paths []string
	for _, registration := range registrations {
		references, uncertain := referencedPaths(root, registration)
		if uncertain {
			return nil, true
		}
		paths = append(paths, references...)
	}
	return paths, false
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

func readRootDirectoryEntries(root *os.Root) ([]fs.DirEntry, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, errors.Join(readErr, closeErr)
}

func openVerifiedGeneration(parent *os.Root, name, rootPath string, operations packageCleanupOperations) (*os.Root, bool) {
	if name == "" || name == "." || name == ".." || hostpath.Base(name) != name {
		return nil, false
	}
	path := hostpath.Join(rootPath, name)
	if operations.beforeGenerationOpen != nil {
		if err := operations.beforeGenerationOpen(path); err != nil {
			return nil, false
		}
	}
	before, err := parent.Lstat(name)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, false
	}
	if operations.afterGenerationLstat != nil {
		operations.afterGenerationLstat(path)
	}
	generation, err := parent.OpenRoot(name)
	if err != nil {
		return nil, false
	}
	opened, err := generation.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		_ = generation.Close()
		return nil, false
	}
	if operations.afterGenerationVerified != nil {
		operations.afterGenerationVerified(path)
	}
	return generation, true
}

func removePackageGenerationAtRoot(parent *os.Root, generation verifiedPackageGeneration, rootPath string, operations packageCleanupOperations) error {
	path := hostpath.Join(rootPath, generation.name)
	if generation.root == nil || generation.fileInfo == nil {
		return fmt.Errorf("package generation %s has no verified directory handle", path)
	}
	entries, err := readRootDirectoryEntries(generation.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read package generation %s: %w", path, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if operations.afterGenerationEnumerate != nil {
		operations.afterGenerationEnumerate(path)
	}
	var payloadErr error
	for _, entry := range entries {
		name := entry.Name()
		if name == packageInstallReceiptFilename {
			continue
		}
		childPath := hostpath.Join(path, name)
		info, statErr := generation.root.Lstat(name)
		if statErr != nil {
			payloadErr = errors.Join(payloadErr, fmt.Errorf("inspect package generation entry %s: %w", childPath, statErr))
			continue
		}
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			if operations.beforeRemoveAll != nil {
				if err := operations.beforeRemoveAll(childPath); err != nil {
					payloadErr = errors.Join(payloadErr, err)
					continue
				}
			}
			payloadErr = errors.Join(payloadErr, generation.root.RemoveAll(name))
		} else {
			if operations.beforeRemove != nil {
				if err := operations.beforeRemove(childPath); err != nil {
					payloadErr = errors.Join(payloadErr, err)
					continue
				}
			}
			payloadErr = errors.Join(payloadErr, generation.root.Remove(name))
		}
	}
	if payloadErr != nil {
		return fmt.Errorf("remove package generation payload %s: %w", path, payloadErr)
	}
	remaining, err := readRootDirectoryEntries(generation.root)
	if err != nil {
		return fmt.Errorf("verify package generation payload removal %s: %w", path, err)
	}
	for _, entry := range remaining {
		if entry.Name() != packageInstallReceiptFilename {
			return fmt.Errorf("package generation %s gained an unowned entry during cleanup: %s", path, entry.Name())
		}
	}
	if operations.beforeRemove != nil {
		if err := operations.beforeRemove(hostpath.Join(path, packageInstallReceiptFilename)); err != nil {
			return fmt.Errorf("remove package generation receipt %s: %w", path, err)
		}
	}
	if err := generation.root.Remove(packageInstallReceiptFilename); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove package generation receipt %s: %w", path, err)
	}
	if operations.beforeGenerationRemove != nil {
		operations.beforeGenerationRemove(path)
	}
	if err := generation.root.Close(); err != nil {
		return fmt.Errorf("close package generation %s before final removal: %w", path, err)
	}
	generation.root = nil
	current, err := parent.Lstat(generation.name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify package generation directory %s before removal: %w", path, err)
	}
	if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, generation.fileInfo) {
		return fmt.Errorf("package generation directory %s changed before final removal", path)
	}
	if err := parent.Remove(generation.name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove package generation directory %s: %w", path, err)
	}
	return nil
}

func hasTransactionEvidence(entries []fs.DirEntry, runtimeID string) bool {
	for _, entry := range entries {
		generationID, valid := parsePackageGenerationName(entry.Name())
		if valid && sameRuntimeID(generationID, runtimeID) {
			return true
		}
	}
	return false
}

func hasReservedTransactionAncestor(path string) bool {
	absolute, err := hostpath.Abs(path)
	if err != nil {
		return true
	}
	for current := hostpath.Clean(absolute); ; current = hostpath.Dir(current) {
		name := hostpath.Base(current)
		prefix := ".dbc-package-"
		reservedName := len(name) > len(prefix) && strings.EqualFold(name[:len(prefix)], prefix)
		if reservedName {
			return true
		}
		parent := hostpath.Dir(current)
		if parent == current {
			break
		}
	}
	return false
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
	if !hostpath.IsAbs(path) {
		path = hostpath.Join(root, path)
	}
	resolved, err := hostpath.Abs(hostpath.Clean(path))
	if err != nil {
		return hostpath.Clean(path)
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
	resolvedParent, parentCertain := canonicalFilesystemPath(parent)
	resolvedChild, childCertain := canonicalFilesystemPath(child)
	if !parentCertain || !childCertain {
		return true
	}
	parentInfo, err := os.Stat(resolvedParent)
	if err != nil || !parentInfo.IsDir() {
		return true
	}
	for current := resolvedChild; ; current = hostpath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil {
			if info.IsDir() && os.SameFile(parentInfo, info) {
				return true
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return true
		}
		parentPath := hostpath.Dir(current)
		if parentPath == current {
			return false
		}
	}
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
	if hostpath.IsWindows() {
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
	infoA, errA := os.Stat(resolvedA)
	infoB, errB := os.Stat(resolvedB)
	if errA == nil && errB == nil {
		return os.SameFile(infoA, infoB), true
	}
	missingA := errors.Is(errA, fs.ErrNotExist)
	missingB := errors.Is(errB, fs.ErrNotExist)
	if errA != nil && !missingA || errB != nil && !missingB {
		return false, false
	}
	if missingA && missingB {
		if hostpath.Equal(resolvedA, resolvedB) {
			return true, true
		}
		return false, false
	}
	// An existing path and a missing path cannot identify the same current
	// filesystem object, even when their spellings differ only by case.
	return false, true
}

func canonicalFilesystemPath(path string) (string, bool) {
	absolute, err := hostpath.Abs(hostpath.Clean(path))
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
			resolved, resolveErr := hostpath.EvalSymlinks(current)
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
				resolved = hostpath.Join(resolved, suffix[i])
			}
			return hostpath.Clean(resolved), true
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			return "", false
		}
		parent := hostpath.Dir(current)
		if parent == current {
			return "", false
		}
		suffix = append(suffix, hostpath.Base(current))
		current = parent
	}
}
