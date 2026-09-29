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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/internal/fslock"
)

func TestUninstallDriverCleansOwnedGenerationAndPreservesExternalFiles(t *testing.T) {
	for _, externalInsideRoot := range []bool{true, false} {
		name := "external-outside-root"
		if externalInsideRoot {
			name = "external-inside-root"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			externalRoot := t.TempDir()
			if externalInsideRoot {
				externalRoot = filepath.Join(root, "external")
			}
			if err := os.MkdirAll(externalRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(externalRoot, "libexternal.so")
			if err := os.WriteFile(external, []byte("external library"), 0o600); err != nil {
				t.Fatal(err)
			}
			sibling := filepath.Join(externalRoot, "user-data.txt")
			if err := os.WriteFile(sibling, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			manifest := fmt.Sprintf("name = \"External\"\nversion = \"1.0.0\"\n[Driver]\nshared = %q\n", external)
			archive := writeCustomPackageArchive(t, manifest, packageFile("NOTICE", "package metadata"))
			if _, err := InstallPackage(cfg, "external", archive, InstallPackageOptions{}); err != nil {
				t.Fatal(err)
			}
			selected, err := GetDriver(cfg, "external")
			if err != nil {
				t.Fatal(err)
			}
			generation := filepath.Join(root, packageGenerationNames(t, root, "external")[0])
			if err := UninstallDriver(cfg, selected); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(generation); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("metadata generation remains: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "external.toml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("registration remains: %v", err)
			}
			for _, path := range []string{external, sibling} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("external or sibling file %s was removed: %v", path, err)
				}
			}
		})
	}
}

func TestUninstallDriverCleansOwnedPackageGeneration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	selected, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Dir(selected.Driver.Shared.Get(PlatformTuple()))
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generation); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned generation remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "driver.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registration remains: %v", err)
	}
}

func TestUninstallDriverRetainsUnprovenGeneration(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "fingerprint mismatch"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			installInitialPackage(t, cfg)
			selected, err := GetDriver(cfg, "driver")
			if err != nil {
				t.Fatal(err)
			}
			generation := filepath.Dir(selected.Driver.Shared.Get(PlatformTuple()))
			receiptPath := filepath.Join(generation, packageInstallReceiptFilename)
			switch mode {
			case "missing":
				if err := os.Remove(receiptPath); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(receiptPath, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "fingerprint mismatch":
				selected.Name = "Changed registration"
				if err := createRuntimeRegistrationUnlocked(cfg, selected.FilePath, selected); err != nil {
					t.Fatal(err)
				}
				selected, err = GetDriver(cfg, "driver")
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := UninstallDriver(cfg, selected); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(generation); err != nil {
				t.Fatalf("unproven generation was removed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "driver.toml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("registration remains: %v", err)
			}
		})
	}
}

func TestCleanupIsolatesRegistrationScopesInOnePayloadRoot(t *testing.T) {
	levels := []ConfigLevel{ConfigEnv, ConfigUser, ConfigSystem}
	seen := make(map[packageRegistrationScope]struct{})
	for _, level := range levels {
		cfg := Config{Level: level}
		scope, err := packageRegistrationScopeForConfig(cfg)
		if err != nil {
			continue
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		root := t.TempDir()
		info := fingerprintTestDriverInfo()
		info.ID = "driver"
		info.Source = "dbc"
		info.Driver.Shared.Set(PlatformTuple(), "/external/shared.so")
		fingerprint, err := runtimeRegistrationFingerprint(cfg, info.ID, PlatformTuple(), info, packageLibraryExternal, "/external/shared.so")
		if err != nil {
			t.Fatal(err)
		}
		locations := make(map[packageRegistrationScope]string)
		for _, candidateScope := range []packageRegistrationScope{packageRegistrationFile, packageRegistrationRegistryUser, packageRegistrationRegistrySystem} {
			generation := filepath.Join(root, ".dbc-package-driver-"+string(candidateScope))
			if err := os.Mkdir(generation, 0o700); err != nil {
				t.Fatal(err)
			}
			receipt := packageInstallReceipt{
				SchemaVersion:                packageInstallReceiptVersion,
				RegistrationScope:            candidateScope,
				RuntimeID:                    info.ID,
				DriverVersion:                info.Version.String(),
				Platform:                     PlatformTuple(),
				Generation:                   filepath.Base(generation),
				LibraryKind:                  packageLibraryExternal,
				RegistrationFingerprintAlgo:  registrationFingerprintName,
				RegistrationFingerprintVer:   registrationFingerprintVer,
				RegistrationFingerprintValue: fingerprint,
			}
			data, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(generation, packageInstallReceiptFilename), data, 0o600); err != nil {
				t.Fatal(err)
			}
			locations[candidateScope] = generation
		}
		if err := cleanupInstalledPackage(cfg, root, info, os.Remove, os.RemoveAll); err != nil {
			t.Fatal(err)
		}
		for candidateScope, generation := range locations {
			_, err := os.Stat(generation)
			if candidateScope == scope {
				if !errors.Is(err, os.ErrNotExist) {
					t.Errorf("scope %q candidate remains: %v", candidateScope, err)
				}
			} else if err != nil {
				t.Errorf("scope %q candidate was removed across scope %q: %v", candidateScope, scope, err)
			}
		}
		gcGeneration := filepath.Join(root, ".dbc-package-driver-gc")
		if err := os.Mkdir(gcGeneration, 0o700); err != nil {
			t.Fatal(err)
		}
		gcReceipt := packageInstallReceipt{
			SchemaVersion:                packageInstallReceiptVersion,
			RegistrationScope:            scope,
			RuntimeID:                    info.ID,
			DriverVersion:                info.Version.String(),
			Platform:                     PlatformTuple(),
			Generation:                   filepath.Base(gcGeneration),
			LibraryKind:                  packageLibraryExternal,
			RegistrationFingerprintAlgo:  registrationFingerprintName,
			RegistrationFingerprintVer:   registrationFingerprintVer,
			RegistrationFingerprintValue: fingerprint,
		}
		data, err := json.Marshal(gcReceipt)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gcGeneration, packageInstallReceiptFilename), data, 0o600); err != nil {
			t.Fatal(err)
		}
		cleanupStalePackageGenerations(cfg, root, info, nil, os.Remove, os.RemoveAll)
		if _, err := os.Stat(gcGeneration); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stale generation in current scope was not collected: %v", err)
		}
	}
}

func TestInstallPackageProtectsGenerationReferencedByNewRegistration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	first, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	firstLibrary := first.Driver.Shared.Get(PlatformTuple())
	manifest := fmt.Sprintf("name = \"Driver\"\nversion = \"2.0.0\"\n[Driver]\nshared = %q\n", firstLibrary)
	archive := writeCustomPackageArchive(t, manifest, packageFile("NOTICE", "metadata"))
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	current, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	if current.Driver.Shared.Get(PlatformTuple()) != firstLibrary {
		t.Fatalf("manifest-only registration points to %q, want %q", current.Driver.Shared.Get(PlatformTuple()), firstLibrary)
	}
	if data, err := os.ReadFile(firstLibrary); err != nil || string(data) != "xxx" {
		t.Fatalf("referenced previous generation was not preserved: %q, %v", data, err)
	}
}

func TestInstallPackageProtectsGenerationReferencedThroughSymlink(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	first, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	firstLibrary := first.Driver.Shared.Get(PlatformTuple())
	firstGeneration := filepath.Dir(firstLibrary)
	link := filepath.Join(root, "generation-link")
	if err := os.Symlink(firstGeneration, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	manifest := fmt.Sprintf("name = \"Driver\"\nversion = \"2.0.0\"\n[Driver]\nshared = %q\n", filepath.Join(link, filepath.Base(firstLibrary)))
	archive := writeCustomPackageArchive(t, manifest)
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	if _, err := os.Stat(firstLibrary); err != nil {
		t.Fatalf("symlink-referenced generation was removed: %v", err)
	}
}

func TestInstallPackageProtectsGenerationAcrossPayloadRootSymlink(t *testing.T) {
	actualRoot := t.TempDir()
	actualCfg := Config{Level: ConfigEnv, Location: actualRoot}
	installInitialPackage(t, actualCfg)
	first, err := GetDriver(actualCfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	firstLibrary := first.Driver.Shared.Get(PlatformTuple())
	rootAlias := filepath.Join(t.TempDir(), "root-alias")
	if err := os.Symlink(actualRoot, rootAlias); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	aliasCfg := Config{Level: ConfigEnv, Location: rootAlias}
	manifest := fmt.Sprintf("name = \"Driver\"\nversion = \"2.0.0\"\n[Driver]\nshared = %q\n", firstLibrary)
	archive := writeCustomPackageArchive(t, manifest)
	if _, err := InstallPackage(aliasCfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	if _, err := os.Stat(firstLibrary); err != nil {
		t.Fatalf("payload-root-alias referenced generation was removed: %v", err)
	}
}

func TestInstallPackageConservativelyProtectsParentTraversalReference(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	first, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	firstLibrary := first.Driver.Shared.Get(PlatformTuple())
	firstGeneration := filepath.Dir(firstLibrary)
	subdirectory := filepath.Join(firstGeneration, "subdir")
	if err := os.Mkdir(subdirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "generation-subdir")
	if err := os.Symlink(subdirectory, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	reference := link + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(firstLibrary)
	if _, err := os.Stat(reference); err != nil {
		t.Fatalf("fixture parent-traversal reference does not resolve to the library: %v", err)
	}
	manifest := fmt.Sprintf("name = \"Driver\"\nversion = \"2.0.0\"\n[Driver]\nshared = %q\n", reference)
	archive := writeCustomPackageArchive(t, manifest)
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	if _, err := os.Stat(firstLibrary); err != nil {
		t.Fatalf("generation referenced through symlink and parent component was removed: %v", err)
	}
}

func TestUncertainSymlinkReferenceProtectsGeneration(t *testing.T) {
	root := t.TempDir()
	generation := filepath.Join(root, ".dbc-package-driver-generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	brokenLink := filepath.Join(root, "broken-link")
	if err := os.Symlink(filepath.Join(root, "missing-target"), brokenLink); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if !generationReferenced(generation, []string{filepath.Join(brokenLink, "lib.so")}) {
		t.Fatal("uncertain symlink reference did not conservatively protect generation")
	}
}

func TestRemovePackageGenerationKeepsReceiptUntilPayloadIsRemoved(t *testing.T) {
	root := t.TempDir()
	generation := filepath.Join(root, ".dbc-package-driver-test")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(generation, "driver.so")
	if err := os.WriteFile(payload, []byte("library"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generation, packageInstallReceiptFilename), []byte("receipt"), 0o600); err != nil {
		t.Fatal(err)
	}
	var removed []string
	payloadErr := errors.New("loaded DLL")
	err := removePackageGeneration(generation, func(path string) error {
		removed = append(removed, filepath.Base(path))
		if path == payload {
			return payloadErr
		}
		return os.Remove(path)
	}, os.RemoveAll)
	if !errors.Is(err, payloadErr) {
		t.Fatalf("remove generation error = %v", err)
	}
	if len(removed) != 1 || removed[0] != "driver.so" {
		t.Fatalf("remove order = %v, receipt must not be removed after payload failure", removed)
	}
	if _, err := os.Stat(filepath.Join(generation, packageInstallReceiptFilename)); err != nil {
		t.Fatalf("receipt was not retained: %v", err)
	}
}

func TestRemovePackageGenerationDeletesReceiptAfterPayload(t *testing.T) {
	root := t.TempDir()
	generation := filepath.Join(root, ".dbc-package-driver-test")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"driver.so", "NOTICE"} {
		if err := os.WriteFile(filepath.Join(generation, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(generation, packageInstallReceiptFilename), []byte("receipt"), 0o600); err != nil {
		t.Fatal(err)
	}
	var removed []string
	remove := func(path string) error {
		removed = append(removed, filepath.Base(path))
		return os.Remove(path)
	}
	if err := removePackageGeneration(generation, remove, os.RemoveAll); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 || removed[2] != packageInstallReceiptFilename {
		t.Fatalf("removal order = %v, want receipt last", removed)
	}
	if _, err := os.Stat(generation); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generation remains after cleanup: %v", err)
	}
}

func TestUninstallPayloadFailureKeepsRegistrationAndReceipt(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	selected, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Dir(selected.Driver.Shared.Get(PlatformTuple()))
	cleanupErr := errors.New("loaded package library")
	operations := packageCleanupOperations{
		remove: func(path string) error {
			if path == selected.Driver.Shared.Get(PlatformTuple()) {
				return cleanupErr
			}
			return os.Remove(path)
		},
		removeAll: os.RemoveAll,
	}
	if err := uninstallDriverUnlockedWithCleanup(cfg, selected, operations); !errors.Is(err, cleanupErr) {
		t.Fatalf("uninstall error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "driver.toml")); err != nil {
		t.Fatalf("registration was removed after payload failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(generation, packageInstallReceiptFilename)); err != nil {
		t.Fatalf("ownership receipt was removed after payload failure: %v", err)
	}
}

func TestUninstallRetriesStaleReceiptGenerationsBestEffort(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	old, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := filepath.Dir(old.Driver.Shared.Get(PlatformTuple()))
	archive := testPackageArchive(t, "updated")
	installOps := testPackageInstallOperations()
	installOps.remove = func(path string) error {
		if filepath.Dir(path) == oldGeneration {
			return errors.New("old library is still loaded")
		}
		return os.Remove(path)
	}
	if _, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, installOps); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	current, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration := filepath.Dir(current.Driver.Shared.Get(PlatformTuple()))
	if _, ok := readPackageInstallReceipt(root, oldGeneration); !ok {
		t.Fatal("old generation receipt was not retained after install GC failure")
	}
	if err := UninstallDriver(cfg, current); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldGeneration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old generation was not retried during uninstall: %v", err)
	}
	if _, err := os.Stat(currentGeneration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("current generation remains after uninstall: %v", err)
	}
}

func TestUninstallStaleCleanupFailureDoesNotBlockRegistrationRemoval(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	old, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := filepath.Dir(old.Driver.Shared.Get(PlatformTuple()))
	archive := testPackageArchive(t, "updated")
	installOps := testPackageInstallOperations()
	installOps.remove = func(path string) error {
		if filepath.Dir(path) == oldGeneration {
			return errors.New("old library is still loaded")
		}
		return os.Remove(path)
	}
	if _, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, installOps); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	current, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	staleErr := errors.New("stale library remains loaded")
	uninstallOps := packageCleanupOperations{
		remove: func(path string) error {
			if filepath.Dir(path) == oldGeneration {
				return staleErr
			}
			return os.Remove(path)
		},
		removeAll: os.RemoveAll,
	}
	if err := uninstallDriverUnlockedWithCleanup(cfg, current, uninstallOps); err != nil {
		t.Fatalf("stale cleanup failure blocked uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "driver.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registration remains after stale cleanup failure: %v", err)
	}
	if _, ok := readPackageInstallReceipt(root, oldGeneration); !ok {
		t.Fatal("stale failure did not retain the receipt")
	}
}

func TestInstallPackageCleansStaleReceiptGenerationsBestEffort(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	old, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := filepath.Dir(old.Driver.Shared.Get(PlatformTuple()))
	archive := testPackageArchive(t, "updated")
	cleanupErr := errors.New("injected loaded library")
	operations := testPackageInstallOperations()
	operations.remove = func(path string) error {
		if filepath.Dir(path) == oldGeneration {
			return cleanupErr
		}
		return os.Remove(path)
	}
	if _, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations); err != nil {
		t.Fatalf("install should succeed when stale cleanup fails: %v", err)
	}
	assertArchiveClosed(t, archive)
	current, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration := filepath.Dir(current.Driver.Shared.Get(PlatformTuple()))
	if sameResolvedFilesystemPath(currentGeneration, oldGeneration) {
		t.Fatal("registration still references old generation")
	}
	if _, ok := readPackageInstallReceipt(root, oldGeneration); !ok {
		t.Fatal("failed stale cleanup did not preserve old ownership receipt")
	}
	if _, ok := readPackageInstallReceipt(root, currentGeneration); !ok {
		t.Fatal("current generation receipt is missing")
	}
}

func TestInstallPackageRemovesStaleReceiptGeneration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	old, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := filepath.Dir(old.Driver.Shared.Get(PlatformTuple()))
	archive := testPackageArchive(t, "updated")
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	if _, err := os.Stat(oldGeneration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale generation remains after successful cleanup: %v", err)
	}
}

func makeNamespaceSiblingInfo(id, root string) DriverInfo {
	info := DriverInfo{
		ID:        id,
		FilePath:  root,
		Name:      "Sibling Driver",
		Publisher: "Test Publisher",
		License:   "MIT",
		Version:   semver.MustParse("1.0.0"),
		Source:    "external",
	}
	info.Driver.Shared.Set(PlatformTuple(), filepath.Join(root, id, "driver.so"))
	return info
}

func TestInstallPackageStaleCleanupProtectsGenerationReferencedBySiblingDriver(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	old, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := filepath.Dir(old.Driver.Shared.Get(PlatformTuple()))

	sibling := makeNamespaceSiblingInfo("sibling", root)
	sibling.Source = "external"
	sibling.Driver.Shared.Set(PlatformTuple(), old.Driver.Shared.Get(PlatformTuple()))
	if err := CreateManifest(cfg, sibling); err != nil {
		t.Fatal(err)
	}

	archive := testPackageArchive(t, "updated")
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	if _, ok := readPackageInstallReceipt(root, oldGeneration); !ok {
		t.Fatal("stale generation referenced by sibling registration was removed")
	}
}

func TestUninstallKeepsGenerationReferencedBySiblingDriver(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	owned, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Dir(owned.Driver.Shared.Get(PlatformTuple()))

	sibling := makeNamespaceSiblingInfo("sibling", root)
	sibling.Source = "external"
	sibling.Driver.Shared.Set(PlatformTuple(), owned.Driver.Shared.Get(PlatformTuple()))
	if err := CreateManifest(cfg, sibling); err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriver(cfg, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "driver.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstalled registration remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sibling.toml")); err != nil {
		t.Fatalf("sibling registration was removed: %v", err)
	}
	if _, ok := readPackageInstallReceipt(root, generation); !ok {
		t.Fatal("generation referenced by sibling registration was removed")
	}
}

func TestUninstallDriverSharedRejectsDBCRegistrationsWithoutMutation(t *testing.T) {
	for _, kind := range []string{"legacy", "transaction", "manifest-only"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			var info DriverInfo
			var err error
			var payloadPaths []string
			switch kind {
			case "legacy":
				version := semver.MustParse("1.2.3")
				generation := filepath.Join(root, "driver_"+PlatformTuple()+"_v"+version.String())
				if err := os.Mkdir(generation, 0o700); err != nil {
					t.Fatal(err)
				}
				library := filepath.Join(generation, "driver.so")
				if err := os.WriteFile(library, []byte("legacy"), 0o600); err != nil {
					t.Fatal(err)
				}
				info = DriverInfo{ID: "driver", FilePath: root, Name: "Driver", Source: "dbc", Version: version}
				info.Driver.Shared.Set(PlatformTuple(), library)
				payloadPaths = []string{generation, library}
				if err := CreateManifest(cfg, info); err != nil {
					t.Fatal(err)
				}
			case "transaction":
				installInitialPackage(t, cfg)
				info, err = GetDriver(cfg, "driver")
				if err != nil {
					t.Fatal(err)
				}
				payloadPaths = []string{filepath.Dir(info.Driver.Shared.Get(PlatformTuple())), info.Driver.Shared.Get(PlatformTuple())}
			case "manifest-only":
				external := filepath.Join(t.TempDir(), "external.so")
				if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
					t.Fatal(err)
				}
				archive := writeCustomPackageArchive(t, fmt.Sprintf("name = \"Driver\"\nversion = \"1.0.0\"\n[Driver]\nshared = %q\n", external), packageFile("NOTICE", "metadata"))
				if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
					t.Fatal(err)
				}
				assertArchiveClosed(t, archive)
				info, err = GetDriver(cfg, "driver")
				if err != nil {
					t.Fatal(err)
				}
				generationNames := packageGenerationNames(t, root, "driver")
				if len(generationNames) != 1 {
					t.Fatalf("manifest-only generation count = %d, want 1", len(generationNames))
				}
				payloadPaths = []string{filepath.Join(root, generationNames[0]), external}
			}
			if kind != "legacy" {
				// The install path created the registration before returning info.
				if _, err := os.Stat(filepath.Join(root, "driver.toml")); err != nil {
					t.Fatal(err)
				}
			}
			if err := UninstallDriverShared(info); err == nil || !strings.Contains(err.Error(), "use UninstallDriver(cfg, info)") {
				t.Fatalf("UninstallDriverShared error = %v, want config-aware uninstall guidance", err)
			}
			if _, err := os.Stat(filepath.Join(root, "driver.toml")); err != nil {
				t.Fatalf("UninstallDriverShared changed the registration: %v", err)
			}
			for _, path := range payloadPaths {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("UninstallDriverShared changed payload %s: %v", path, err)
				}
			}
		})
	}
}

func TestBorrowerUninstallRetainsAnotherDriversTransactionGeneration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	owned, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Dir(owned.Driver.Shared.Get(PlatformTuple()))
	borrower := makeNamespaceSiblingInfo("borrower", root)
	borrower.Source = "external"
	borrower.Driver.Shared.Set(PlatformTuple(), owned.Driver.Shared.Get(PlatformTuple()))
	if err := CreateManifest(cfg, borrower); err != nil {
		t.Fatal(err)
	}
	selected, err := GetDriver(cfg, borrower.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriverShared(selected); err != nil {
		t.Fatalf("public borrower cleanup: %v", err)
	}
	if _, ok := readPackageInstallReceipt(root, generation); !ok {
		t.Fatal("public borrower cleanup removed the owner's generation")
	}
	if _, err := os.Stat(filepath.Join(root, "borrower.toml")); err != nil {
		t.Fatalf("public shared cleanup removed the borrower registration: %v", err)
	}
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatalf("config-aware borrower uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "borrower.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("borrower registration remains after uninstall: %v", err)
	}
	if _, ok := readPackageInstallReceipt(root, generation); !ok {
		t.Fatal("borrower uninstall removed the owner's generation")
	}
}

func TestNonDBCCleanupRetainsReservedTransactionGeneration(t *testing.T) {
	for _, referenceKind := range []string{"direct", "missing-receipt", "corrupt-receipt", "symlink-alias", "parent-traversal"} {
		t.Run(referenceKind, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			installInitialPackage(t, cfg)
			owner, err := GetDriver(cfg, "driver")
			if err != nil {
				t.Fatal(err)
			}
			library := owner.Driver.Shared.Get(PlatformTuple())
			generation := filepath.Dir(library)
			if referenceKind == "missing-receipt" {
				if err := os.Remove(filepath.Join(generation, packageInstallReceiptFilename)); err != nil {
					t.Fatal(err)
				}
			} else if referenceKind == "corrupt-receipt" {
				if err := os.WriteFile(filepath.Join(generation, packageInstallReceiptFilename), []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			shared := library
			switch referenceKind {
			case "symlink-alias", "parent-traversal":
				subdirectory := filepath.Join(generation, "subdirectory")
				if err := os.Mkdir(subdirectory, 0o700); err != nil {
					t.Fatal(err)
				}
				alias := filepath.Join(root, "generation-alias")
				if err := os.Symlink(subdirectory, alias); err != nil {
					t.Skipf("symlink creation is unavailable: %v", err)
				}
				if referenceKind == "symlink-alias" {
					if err := os.Remove(alias); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(generation, alias); err != nil {
						t.Fatal(err)
					}
					shared = filepath.Join(alias, filepath.Base(library))
				} else {
					shared = alias + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(library)
				}
				if _, err := os.Stat(shared); err != nil {
					t.Fatalf("fixture alias does not resolve to library: %v", err)
				}
			}

			borrower := makeNamespaceSiblingInfo("borrower", root)
			borrower.Source = "external"
			borrower.Driver.Shared.Set(PlatformTuple(), shared)
			if err := CreateManifest(cfg, borrower); err != nil {
				t.Fatal(err)
			}
			selected, err := GetDriver(cfg, borrower.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := UninstallDriverShared(selected); err != nil {
				t.Fatalf("public borrower cleanup: %v", err)
			}
			if _, err := os.Stat(library); err != nil {
				t.Fatalf("public helper removed transaction-owned library: %v", err)
			}
			if err := UninstallDriver(cfg, selected); err != nil {
				t.Fatalf("borrower uninstall: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "borrower.toml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("borrower registration remains: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "driver.toml")); err != nil {
				t.Fatalf("owner registration was removed: %v", err)
			}
			if _, err := os.Stat(generation); err != nil {
				t.Fatalf("borrower cleanup removed owner generation: %v", err)
			}
		})
	}
}

func TestNonDBCCleanupUsesSameRelativePathForGuardAndRemoval(t *testing.T) {
	root := t.TempDir()
	drivers := filepath.Join(root, "drivers")
	if err := os.Mkdir(drivers, 0o700); err != nil {
		t.Fatal(err)
	}
	generation := filepath.Join(drivers, ".dbc-package-owner-generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(generation, "lib.so")
	if err := os.WriteFile(library, []byte("managed"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(drivers, "alias")
	if err := os.Symlink(filepath.Base(generation), alias); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(workingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()
	info := DriverInfo{ID: "borrower", FilePath: "drivers", Source: "external"}
	info.Driver.Shared.Set(PlatformTuple(), "drivers/alias/lib.so")
	if err := UninstallDriverShared(info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(library); err != nil {
		t.Fatalf("relative symlink alias removed managed library: %v", err)
	}
}

func TestNonDBCCleanupRetainsDerivedParentTraversalPath(t *testing.T) {
	root := t.TempDir()
	drivers := filepath.Join(root, "drivers")
	if err := os.Mkdir(drivers, 0o700); err != nil {
		t.Fatal(err)
	}
	generation := filepath.Join(root, ".dbc-package-owner-generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(generation, "lib.so")
	if err := os.WriteFile(library, []byte("managed"), 0o600); err != nil {
		t.Fatal(err)
	}
	info := DriverInfo{ID: "borrower", FilePath: drivers, Source: "external"}
	info.Driver.Shared.Set(PlatformTuple(), library)
	if err := UninstallDriverShared(info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(library); err != nil {
		t.Fatalf("derived parent-traversal path removed managed library: %v", err)
	}
}

func TestNonDBCCleanupDoesNotGuessManifestOnlyExtraFolder(t *testing.T) {
	root := t.TempDir()
	library := filepath.Join(root, "driver.so")
	if err := os.WriteFile(library, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	extraFolder := filepath.Join(root, "external_"+PlatformTuple()+"_v1.2.3")
	if err := os.Mkdir(extraFolder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extraFolder, "NOTICE"), []byte("metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	info := DriverInfo{ID: "external", FilePath: root, Source: "external", Version: semver.MustParse("1.2.3")}
	info.Driver.Shared.Set(PlatformTuple(), library)
	if err := UninstallDriverShared(info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(library); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ordinary shared library remains: %v", err)
	}
	if _, err := os.Stat(extraFolder); err != nil {
		t.Fatalf("guessed metadata folder was removed: %v", err)
	}
}

func TestRegistrationNamespaceLockSerializesInstallCleanupAndCreateManifest(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	old, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := filepath.Dir(old.Driver.Shared.Get(PlatformTuple()))
	cleanupStarted := make(chan struct{})
	allowCleanup := make(chan struct{})
	archive := testPackageArchive(t, "updated")
	operations := testPackageInstallOperations()
	operations.remove = func(path string) error {
		if filepath.Dir(path) == oldGeneration {
			select {
			case <-cleanupStarted:
			default:
				close(cleanupStarted)
			}
			<-allowCleanup
		}
		return os.Remove(path)
	}
	installDone := make(chan error, 1)
	go func() {
		_, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations)
		installDone <- err
	}()
	<-cleanupStarted

	sibling := makeNamespaceSiblingInfo("sibling", root)
	sibling.Source = "external"
	sibling.Driver.Shared.Set(PlatformTuple(), old.Driver.Shared.Get(PlatformTuple()))
	siblingDone := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		siblingDone <- CreateManifest(cfg, sibling)
	}()
	<-started
	deadline := time.Now().Add(time.Second)
	for {
		probe, probeErr := acquireDriverInstallLockWith(context.Background(), root, "sibling", 5*time.Millisecond)
		if errors.Is(probeErr, fslock.ErrLockContended) {
			break
		}
		if probeErr != nil {
			t.Fatalf("probe sibling driver lock: %v", probeErr)
		}
		if err := probe.release(); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("sibling registration did not acquire its driver lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case err := <-siblingDone:
		t.Fatalf("sibling registration published during stale cleanup: %v", err)
	default:
	}
	close(allowCleanup)
	if err := <-installDone; err != nil {
		t.Fatalf("update install: %v", err)
	}
	if err := <-siblingDone; err != nil {
		t.Fatalf("sibling manifest creation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sibling.toml")); err != nil {
		t.Fatalf("sibling registration was not published after cleanup: %v", err)
	}
}

func TestUncertainSiblingRegistrationSkipsPayloadCleanupButAllowsUninstall(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	old, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := filepath.Dir(old.Driver.Shared.Get(PlatformTuple()))
	if err := os.WriteFile(filepath.Join(root, "broken.toml"), []byte("[Driver\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	archive := testPackageArchive(t, "updated")
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatalf("install should continue when cleanup enumeration is uncertain: %v", err)
	}
	assertArchiveClosed(t, archive)
	if _, ok := readPackageInstallReceipt(root, oldGeneration); !ok {
		t.Fatal("uncertain registration enumeration did not conservatively retain stale generation")
	}
	current, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration := filepath.Dir(current.Driver.Shared.Get(PlatformTuple()))
	if err := UninstallDriver(cfg, current); err != nil {
		t.Fatalf("uninstall should remove registration despite uncertain cleanup enumeration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "driver.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registration remains after uninstall: %v", err)
	}
	for _, generation := range []string{oldGeneration, currentGeneration} {
		if _, ok := readPackageInstallReceipt(root, generation); !ok {
			t.Errorf("unproven cleanup removed generation %s", generation)
		}
	}
}

func TestInstallPackageCleansRollbackCandidateOnLaterSuccess(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := testPackageArchive(t, "candidate")
	operations := testPackageInstallOperations()
	var candidate string
	operations.register = func(_ Config, _ string, info DriverInfo) error {
		candidate = filepath.Dir(info.Driver.Shared.Get(PlatformTuple()))
		return errors.Join(errors.New("registry failed"), errRegistrationRollbackFailed)
	}
	if _, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations); !errors.Is(err, errRegistrationRollbackFailed) {
		t.Fatalf("initial install error = %v", err)
	}
	if _, ok := readPackageInstallReceipt(root, candidate); !ok {
		t.Fatal("rollback candidate receipt is invalid")
	}
	archive = testPackageArchive(t, "success")
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	if _, err := os.Stat(candidate); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale rollback candidate remains: %v", err)
	}
}

func TestLegacyPackageCleanupRequiresKnownDirectLayout(t *testing.T) {
	for _, basename := range []string{"driver_" + PlatformTuple() + "_v1.2.3", "unknown-layout"} {
		t.Run(basename, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			generation := filepath.Join(root, basename)
			if err := os.Mkdir(generation, 0o700); err != nil {
				t.Fatal(err)
			}
			shared := filepath.Join(generation, "driver.so")
			if err := os.WriteFile(shared, []byte("legacy"), 0o600); err != nil {
				t.Fatal(err)
			}
			info := DriverInfo{ID: "driver", FilePath: root, Name: "Driver", Source: "dbc", Version: semver.MustParse("1.2.3")}
			info.Driver.Shared.Set(PlatformTuple(), shared)
			if err := CreateManifest(cfg, info); err != nil {
				t.Fatal(err)
			}
			selected, err := GetDriver(cfg, info.ID)
			if err != nil {
				t.Fatal(err)
			}
			cleanupErr := UninstallDriver(cfg, selected)
			_, err = os.Stat(generation)
			if cleanupErr != nil {
				t.Fatal(cleanupErr)
			}
			if basename == "driver_"+PlatformTuple()+"_v1.2.3" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("known legacy generation remains: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("unknown generation was removed: %v", err)
				}
			}
		})
	}
}

func TestLegacyPackageCleanupRetainsCorruptReceipt(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	generation := filepath.Join(root, "driver_"+PlatformTuple()+"_v1.2.3")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(generation, "driver.so")
	if err := os.WriteFile(shared, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generation, packageInstallReceiptFilename), []byte("bad receipt"), 0o600); err != nil {
		t.Fatal(err)
	}
	info := DriverInfo{ID: "driver", FilePath: root, Name: "Driver", Source: "dbc", Version: semver.MustParse("1.2.3")}
	info.Driver.Shared.Set(PlatformTuple(), shared)
	if err := CreateManifest(cfg, info); err != nil {
		t.Fatal(err)
	}
	selected, err := GetDriver(cfg, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generation); err != nil {
		t.Fatalf("generation with corrupt receipt was removed: %v", err)
	}
}

func TestLegacyPackageCleanupPreservesGenerationReferencedBySibling(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	version := semver.MustParse("1.2.3")
	generation := filepath.Join(root, "driver_"+PlatformTuple()+"_v"+version.String())
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(generation, "driver.so")
	if err := os.WriteFile(library, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := DriverInfo{ID: "driver", Name: "Driver", Version: version, Source: "dbc"}
	owner.Driver.Shared.Set(PlatformTuple(), library)
	if err := CreateManifest(cfg, owner); err != nil {
		t.Fatal(err)
	}
	borrower := makeNamespaceSiblingInfo("borrower", root)
	borrower.Source = "external"
	borrower.Driver.Shared.Set(PlatformTuple(), library)
	if err := CreateManifest(cfg, borrower); err != nil {
		t.Fatal(err)
	}
	selected, err := GetDriver(cfg, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generation); err != nil {
		t.Fatalf("legacy generation referenced by sibling was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "driver.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owner registration remains after uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "borrower.toml")); err != nil {
		t.Fatalf("sibling registration was removed: %v", err)
	}
}

func TestLegacyManifestOnlySidecarCleanupIsExactAndPreservesExternalLibrary(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	version := semver.MustParse("1.2.3+build.4")
	sidecar := filepath.Join(root, "driver_"+PlatformTuple()+"_v"+version.String())
	if err := os.Mkdir(sidecar, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, "NOTICE"), []byte("metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external", "library.so")
	if err := os.MkdirAll(filepath.Dir(external), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, []byte("external library"), 0o600); err != nil {
		t.Fatal(err)
	}
	info := DriverInfo{ID: "driver", Name: "Driver", Version: version, Source: "dbc"}
	info.Driver.Shared.Set(PlatformTuple(), external)
	if err := CreateManifest(cfg, info); err != nil {
		t.Fatal(err)
	}
	selected, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sidecar); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exact legacy metadata sidecar remains: %v", err)
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatalf("external library was removed: %v", err)
	}
}

func TestLegacyManifestOnlySidecarChecksAllPlatformReferences(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	version := semver.MustParse("1.2.3")
	sidecar := filepath.Join(root, "driver_"+PlatformTuple()+"_v"+version.String())
	if err := os.Mkdir(sidecar, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, "NOTICE"), []byte("metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external", "library.so")
	if err := os.MkdirAll(filepath.Dir(external), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, []byte("external library"), 0o600); err != nil {
		t.Fatal(err)
	}
	info := DriverInfo{ID: "driver", FilePath: root, Name: "Driver", Version: version, Source: "dbc"}
	info.Driver.Shared.Set(PlatformTuple(), external)
	info.Driver.Shared.Set("other_platform", filepath.Join(sidecar, "library.so"))
	if err := CreateManifest(cfg, info); err != nil {
		t.Fatal(err)
	}
	selected, err := GetDriver(cfg, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar referenced by another platform was removed: %v", err)
	}
}

func TestLegacyManifestOnlySidecarProtectsSymlinkParentTraversalReference(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	version := semver.MustParse("1.2.3")
	sidecar := filepath.Join(root, "driver_"+PlatformTuple()+"_v"+version.String())
	subdirectory := filepath.Join(sidecar, "subdir")
	if err := os.MkdirAll(subdirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(sidecar, "library.so")
	if err := os.WriteFile(external, []byte("external library"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, "sibling.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "sidecar-subdir")
	if err := os.Symlink(subdirectory, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	reference := link + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(external)
	if _, err := os.Stat(reference); err != nil {
		t.Fatalf("fixture parent-traversal reference does not resolve to the library: %v", err)
	}
	info := DriverInfo{ID: "driver", FilePath: root, Name: "Driver", Version: version, Source: "dbc"}
	info.Driver.Shared.Set(PlatformTuple(), reference)
	if err := CreateManifest(cfg, info); err != nil {
		t.Fatal(err)
	}
	selected, err := GetDriver(cfg, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{sidecar, external, filepath.Join(sidecar, "sibling.txt")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("sidecar data %s was removed through parent traversal: %v", path, err)
		}
	}
}

func TestTransactionEvidenceBlocksLegacyFallback(t *testing.T) {
	for _, receiptState := range []string{"missing", "corrupt"} {
		t.Run(receiptState, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			version := semver.MustParse("1.2.3")
			legacySidecar := filepath.Join(root, "driver_"+PlatformTuple()+"_v"+version.String())
			if err := os.Mkdir(legacySidecar, 0o700); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(legacySidecar, "external.so")
			if err := os.WriteFile(external, []byte("external library"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(legacySidecar, "sibling.txt"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			transaction := filepath.Join(root, ".dbc-package-driver-incomplete")
			if err := os.Mkdir(transaction, 0o700); err != nil {
				t.Fatal(err)
			}
			if receiptState == "corrupt" {
				if err := os.WriteFile(filepath.Join(transaction, packageInstallReceiptFilename), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			info := DriverInfo{ID: "driver", Name: "Driver", Version: version, Source: "dbc"}
			info.Driver.Shared.Set(PlatformTuple(), external)
			if err := CreateManifest(cfg, info); err != nil {
				t.Fatal(err)
			}
			selected, err := GetDriver(cfg, "driver")
			if err != nil {
				t.Fatal(err)
			}
			if err := UninstallDriver(cfg, selected); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{legacySidecar, transaction, external, filepath.Join(legacySidecar, "sibling.txt")} {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("unproven package data %s was removed: %v", path, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "driver.toml")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("registration remains: %v", err)
			}
		})
	}
}

func TestLegacyGenerationVersionRequiresExactCanonicalString(t *testing.T) {
	version := semver.MustParse("1.2.3+build.4")
	if !legacyGenerationNameMatches("driver_"+PlatformTuple()+"_v1.2.3+build.4", "driver", PlatformTuple(), version) {
		t.Fatal("matching build metadata version was rejected")
	}
	if legacyGenerationNameMatches("driver_"+PlatformTuple()+"_v1.2.3", "driver", PlatformTuple(), version) {
		t.Fatal("version with missing build metadata was accepted")
	}
}

func TestUninstallUsesSelectedConfigEnvRootOnly(t *testing.T) {
	primary := filepath.Join(t.TempDir(), "primary")
	secondary := filepath.Join(t.TempDir(), "secondary")
	for _, root := range []string{primary, secondary} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{Level: ConfigEnv, Location: strings.Join([]string{primary, secondary}, string(filepath.ListSeparator))}
	archive := testPackageArchive(t, "library")
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	selected, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	primaryGeneration := filepath.Dir(selected.Driver.Shared.Get(PlatformTuple()))
	secondaryGeneration := filepath.Join(secondary, filepath.Base(primaryGeneration))
	if err := os.Mkdir(secondaryGeneration, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondaryGeneration, "user-file"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(primaryGeneration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected primary generation remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondaryGeneration, "user-file")); err != nil {
		t.Fatalf("secondary root was modified: %v", err)
	}
}

func TestInstallPackageCleansStrictLegacyGenerationOnlyAfterCommit(t *testing.T) {
	for _, failure := range []string{"", "verify", "register"} {
		name := "success"
		if failure != "" {
			name = failure + " failure"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			legacyGeneration, legacyLibrary := createLegacyPackageForInstall(t, cfg, "driver", "0.9.0", "legacy library")
			archive := testPackageArchive(t, "new library")
			var installErr error
			if failure == "register" {
				operations := testPackageInstallOperations()
				operations.register = func(Config, string, DriverInfo) error { return errors.New("registration failed") }
				_, installErr = installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations)
			} else {
				options := InstallPackageOptions{}
				if failure == "verify" {
					options.Verifier = func(string, Manifest) error { return errors.New("verification failed") }
				}
				_, installErr = InstallPackage(cfg, "driver", archive, options)
			}
			if failure == "" {
				if installErr != nil {
					t.Fatal(installErr)
				}
				if _, err := os.Stat(legacyGeneration); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("committed install left legacy generation: %v", err)
				}
				current, err := GetDriver(cfg, "driver")
				if err != nil {
					t.Fatal(err)
				}
				if current.Version.String() != "1.0.0" || current.Driver.Shared.Get(PlatformTuple()) == legacyLibrary {
					t.Fatalf("current registration = %#v", current)
				}
				if _, err := os.Stat(current.Driver.Shared.Get(PlatformTuple())); err != nil {
					t.Fatalf("new library is unavailable: %v", err)
				}
				return
			}
			if installErr == nil {
				t.Fatal("expected installation failure")
			}
			current, err := GetDriver(cfg, "driver")
			if err != nil {
				t.Fatal(err)
			}
			if current.Driver.Shared.Get(PlatformTuple()) != legacyLibrary || current.Version.String() != "0.9.0" {
				t.Fatalf("failed replacement changed registration: %#v", current)
			}
			if data, err := os.ReadFile(legacyLibrary); err != nil || string(data) != "legacy library" {
				t.Fatalf("failed replacement changed legacy payload: %q, %v", data, err)
			}
		})
	}
}

func TestInstallPackageLegacyCleanupFailureDoesNotFailCommit(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	legacyGeneration, _ := createLegacyPackageForInstall(t, cfg, "driver", "0.9.0", "legacy")
	cleanupErr := errors.New("legacy cleanup failed")
	operations := testPackageInstallOperations()
	operations.removeAll = func(path string) error {
		if path == legacyGeneration {
			return cleanupErr
		}
		return os.RemoveAll(path)
	}
	archive := testPackageArchive(t, "new")
	if _, err := installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations); err != nil {
		t.Fatalf("legacy cleanup failure changed install result: %v", err)
	}
	current, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	if current.Version.String() != "1.0.0" {
		t.Fatalf("current version = %s", current.Version)
	}
	if _, err := os.Stat(current.Driver.Shared.Get(PlatformTuple())); err != nil {
		t.Fatalf("committed library is unavailable: %v", err)
	}
	if _, err := os.Stat(legacyGeneration); err != nil {
		t.Fatalf("failed best-effort cleanup did not retain legacy generation: %v", err)
	}
}

func TestInstallPackageRetainsUnprovenLegacyCandidates(t *testing.T) {
	for _, mode := range []string{"malformed registration", "symlink registration", "arbitrary basename", "corrupt legacy receipt", "missing transaction receipt", "corrupt transaction receipt"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			legacyGeneration, _ := createLegacyPackageForInstall(t, cfg, "driver", "0.9.0", "legacy")
			unprovenPath := legacyGeneration
			switch mode {
			case "malformed registration":
				if err := os.WriteFile(filepath.Join(root, "driver.toml"), []byte("not valid ["), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink registration":
				target := filepath.Join(t.TempDir(), "registration.toml")
				registration, err := os.ReadFile(filepath.Join(root, "driver.toml"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, registration, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(root, "driver.toml")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "driver.toml")); err != nil {
					t.Skipf("symlink creation is unavailable: %v", err)
				}
			case "arbitrary basename":
				arbitrary := filepath.Join(root, "release-bundle")
				if err := os.MkdirAll(arbitrary, 0o700); err != nil {
					t.Fatal(err)
				}
				library := filepath.Join(arbitrary, "driver.so")
				if err := os.WriteFile(library, []byte("legacy"), 0o600); err != nil {
					t.Fatal(err)
				}
				info, err := GetDriver(cfg, "driver")
				if err != nil {
					t.Fatal(err)
				}
				info.Driver.Shared.Set(PlatformTuple(), library)
				if err := createRuntimeRegistrationUnlocked(cfg, root, info); err != nil {
					t.Fatal(err)
				}
				unprovenPath = arbitrary
			case "corrupt legacy receipt":
				if err := os.WriteFile(filepath.Join(legacyGeneration, packageInstallReceiptFilename), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing transaction receipt", "corrupt transaction receipt":
				transaction := filepath.Join(root, ".dbc-package-driver-unproven")
				if err := os.Mkdir(transaction, 0o700); err != nil {
					t.Fatal(err)
				}
				if mode == "corrupt transaction receipt" {
					if err := os.WriteFile(filepath.Join(transaction, packageInstallReceiptFilename), []byte("{"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			archive := testPackageArchive(t, "new")
			if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(unprovenPath); err != nil {
				t.Fatalf("unproven candidate %s was removed: %v", unprovenPath, err)
			}
			if mode == "missing transaction receipt" || mode == "corrupt transaction receipt" {
				transaction := filepath.Join(root, ".dbc-package-driver-unproven")
				if _, err := os.Stat(transaction); err != nil {
					t.Fatalf("unproven transaction generation was removed: %v", err)
				}
			}
		})
	}
}

func TestInstallPackageLegacyCleanupRespectsReferencesAndUncertainty(t *testing.T) {
	for _, mode := range []string{"sibling reference", "new manifest-only reference", "uncertain reference snapshot"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			legacyGeneration, legacyLibrary := createLegacyPackageForInstall(t, cfg, "driver", "0.9.0", "legacy")
			if mode == "sibling reference" {
				sibling := DriverInfo{ID: "sibling", Name: "Sibling", Version: semver.MustParse("1.0.0"), Source: "external"}
				sibling.Driver.Shared.Set(PlatformTuple(), legacyLibrary)
				if err := createRuntimeRegistrationUnlocked(cfg, root, sibling); err != nil {
					t.Fatal(err)
				}
			} else if mode == "uncertain reference snapshot" {
				if err := os.WriteFile(filepath.Join(root, "sibling.toml"), []byte("invalid = ["), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var archive *os.File
			if mode == "new manifest-only reference" {
				archive = writeCustomPackageArchive(t, fmt.Sprintf("name = \"Driver\"\nversion = \"1.0.0\"\n[Driver]\nshared = %q\n", legacyLibrary))
			} else {
				archive = testPackageArchive(t, "new")
			}
			if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(legacyGeneration); err != nil {
				t.Fatalf("referenced or uncertain legacy generation was removed: %v", err)
			}
		})
	}
}

func TestInstallPackageLegacyMetadataCleanupPreservesExternalLibrary(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	external := filepath.Join(t.TempDir(), "external.so")
	if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyGeneration := filepath.Join(root, "driver_"+PlatformTuple()+"_v0.9.0")
	if err := os.Mkdir(legacyGeneration, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyGeneration, "NOTICE"), []byte("metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := DriverInfo{ID: "driver", Name: "Driver", Version: semver.MustParse("0.9.0"), Source: "dbc"}
	legacy.Driver.Shared.Set(PlatformTuple(), external)
	if err := createRuntimeRegistrationUnlocked(cfg, root, legacy); err != nil {
		t.Fatal(err)
	}
	archive := testPackageArchive(t, "new")
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyGeneration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy metadata sidecar remains: %v", err)
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "external" {
		t.Fatalf("external library was removed or changed: %q, %v", data, err)
	}
}

func TestInstallPackageLegacyCleanupUsesPrimaryRootOnly(t *testing.T) {
	primary, secondary := t.TempDir(), t.TempDir()
	primaryCfg := Config{Level: ConfigEnv, Location: primary}
	secondaryCfg := Config{Level: ConfigEnv, Location: secondary}
	secondaryGeneration, _ := createLegacyPackageForInstall(t, secondaryCfg, "driver", "0.9.0", "secondary legacy")
	cfg := Config{Level: ConfigEnv, Location: primary + string(filepath.ListSeparator) + secondary}
	archive := testPackageArchive(t, "new")
	if _, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := GetDriver(primaryCfg, "driver"); err != nil {
		t.Fatalf("primary registration was not installed: %v", err)
	}
	if _, err := os.Stat(secondaryGeneration); err != nil {
		t.Fatalf("secondary legacy generation was modified: %v", err)
	}
}

func TestInstallPackageStageRuntimeIDIgnoresOnlyCurrentStagingEntry(t *testing.T) {
	for _, preexistingEvidence := range []bool{false, true} {
		name := "current staging entry only"
		if preexistingEvidence {
			name = "preexisting transaction evidence"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			legacyGeneration, _ := createLegacyPackageForInstall(t, cfg, "stage", "0.9.0", "legacy")
			if preexistingEvidence {
				if err := os.Mkdir(filepath.Join(root, ".dbc-package-stage-preexisting"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			archive := testPackageArchive(t, "new")
			if _, err := InstallPackage(cfg, "stage", archive, InstallPackageOptions{}); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(legacyGeneration)
			if preexistingEvidence && err != nil {
				t.Fatalf("preexisting transaction evidence failed to protect legacy predecessor: %v", err)
			}
			if !preexistingEvidence && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("current staging entry prevented legacy cleanup: %v", err)
			}
		})
	}
}

func createLegacyPackageForInstall(t *testing.T, cfg Config, runtimeID, version, contents string) (string, string) {
	t.Helper()
	root, err := EnsureLocation(cfg)
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Join(root, runtimeID+"_"+PlatformTuple()+"_v"+version)
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(generation, runtimeID+".so")
	if err := os.WriteFile(library, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	info := DriverInfo{ID: runtimeID, Name: "Driver", Version: semver.MustParse(version), Source: "dbc"}
	info.Driver.Shared.Set(PlatformTuple(), library)
	if err := createRuntimeRegistrationUnlocked(cfg, root, info); err != nil {
		t.Fatal(err)
	}
	return generation, library
}
