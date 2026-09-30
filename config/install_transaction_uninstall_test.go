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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestUninstallDriverCleansOnlyRegisteredEnvironmentDirectory(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, location := range []string{first, second} {
		if err := os.MkdirAll(location, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	install := func(location, source string) Manifest {
		t.Helper()
		archive := makeInstallArchive(t, "example", "1.0.0", "library.so", []byte(source+" library"))
		file := writeInstallArchive(t, archive, source)
		manifest, err := InstallPackage(Config{Level: ConfigEnv, Location: location}, "example", file, installExpected("example", source, archive), InstallOptions{})
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		return manifest
	}
	firstManifest := install(first, "first")
	secondManifest := install(second, "second")
	firstLibrary := firstManifest.Driver.Shared.Get(PlatformTuple())
	secondLibrary := secondManifest.Driver.Shared.Get(PlatformTuple())
	secondInfo, err := loadDriverFromManifest(second, "example")
	if err != nil {
		t.Fatal(err)
	}
	combinedConfig := Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second}
	if err := UninstallDriver(combinedConfig, secondInfo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(first, "example.toml")); err != nil {
		t.Fatalf("first-path runtime manifest was removed: %v", err)
	}
	if data, err := os.ReadFile(firstLibrary); err != nil || string(data) != "first library" {
		t.Fatalf("first-path managed generation was removed or changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(second, "example.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second-path runtime manifest remains after uninstall: %v", err)
	}
	if _, err := os.Stat(secondLibrary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second-path managed generation remains after uninstall: %v", err)
	}
}

func TestInstallPackageManifestOnlyKeepsExternalLibrary(t *testing.T) {
	root := t.TempDir()
	externalDir := filepath.Join(root, "external")
	if err := os.Mkdir(externalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	externalLibrary := filepath.Join(externalDir, "driver.so")
	if err := os.WriteFile(externalLibrary, []byte("external library"), 0o644); err != nil {
		t.Fatal(err)
	}
	externalSibling := filepath.Join(externalDir, "LICENSE")
	if err := os.WriteFile(externalSibling, []byte("external license"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyManifest := []byte(fmt.Sprintf(`manifest_version = 1
name = "Manifest Only"
version = "1.0.0"

[Driver]
shared = %q
`, externalLibrary))
	archive := makeInstallArchiveWithEntries(t, installArchiveEntry{name: "MANIFEST", data: legacyManifest})
	cfg := Config{Level: ConfigEnv, Location: root}
	firstFile := writeInstallArchive(t, archive, "legacy-one")
	first, err := InstallPackage(cfg, "manifest-only", firstFile, ExpectedPackageMetadata{ID: "manifest-only", SourceType: "local", SourceIdentity: "local"}, InstallOptions{})
	_ = firstFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Driver.Shared.Get(PlatformTuple()); got != externalLibrary {
		t.Fatalf("external shared path = %q, want %q", got, externalLibrary)
	}
	if got := countInstallGenerations(t, root, "manifest-only"); got != 1 {
		t.Fatalf("manifest-only generation count after first install = %d, want 1", got)
	}
	secondFile := writeInstallArchive(t, archive, "legacy-two")
	second, err := InstallPackage(cfg, "manifest-only", secondFile, ExpectedPackageMetadata{ID: "manifest-only", SourceType: "local", SourceIdentity: "local"}, InstallOptions{})
	_ = secondFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Driver.Shared.Get(PlatformTuple()); got != externalLibrary {
		t.Fatalf("external shared path changed: %q", got)
	}
	if got := countInstallGenerations(t, root, "manifest-only"); got != 1 {
		t.Fatalf("manifest-only generation count after update = %d, want 1", got)
	}
	if got, err := os.ReadFile(externalLibrary); err != nil || string(got) != "external library" {
		t.Fatalf("external library was altered: %q, %v", got, err)
	}
	info, err := GetDriver(cfg, "manifest-only")
	if err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriver(cfg, info); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(externalLibrary); err != nil || string(got) != "external library" {
		t.Fatalf("uninstall altered external library: %q, %v", got, err)
	}
	if got, err := os.ReadFile(externalSibling); err != nil || string(got) != "external license" {
		t.Fatalf("uninstall altered external sibling: %q, %v", got, err)
	}
	if got := countInstallGenerations(t, root, "manifest-only"); got != 0 {
		t.Fatalf("managed receipt generations after uninstall = %d, want 0", got)
	}
}

func TestUninstallDriverRemovesOnlyProvenLegacyPackageGeneration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	legacy := createLegacyInstall(t, root, "example", "1.0.0", "")
	info, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriver(cfg, info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("proven legacy generation remains after uninstall: %v", err)
	}
}

func TestUninstallPackageCleanupReturnsProvenRemovalFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, root string) (Config, DriverInfo, string)
	}{
		{name: "managed generation", setup: func(t *testing.T, root string) (Config, DriverInfo, string) {
			cfg := Config{Level: ConfigEnv, Location: root}
			archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("managed library"))
			file := writeInstallArchive(t, archive, "managed")
			if _, err := InstallPackage(cfg, "example", file, installExpected("example", "source", archive), InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			_ = file.Close()
			info, err := GetDriver(cfg, "example")
			if err != nil {
				t.Fatal(err)
			}
			return cfg, info, filepath.Dir(info.Driver.Shared.Get(PlatformTuple()))
		}},
		{name: "receipt-backed archive basename", setup: func(t *testing.T, root string) (Config, DriverInfo, string) {
			cfg := Config{Level: ConfigEnv, Location: root}
			_, directory := installArchiveBasenamePackage(t, root)
			info, err := GetDriver(cfg, "example")
			if err != nil {
				t.Fatal(err)
			}
			return cfg, info, directory
		}},
		{name: "legacy standard layout", setup: func(t *testing.T, root string) (Config, DriverInfo, string) {
			cfg := Config{Level: ConfigEnv, Location: root}
			directory := createLegacyInstall(t, root, "example", "1.0.0", "")
			info, err := GetDriver(cfg, "example")
			if err != nil {
				t.Fatal(err)
			}
			return cfg, info, directory
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			cfg, info, directory := test.setup(t, root)
			manifest := filepath.Join(info.FilePath, info.ID+".toml")
			if err := os.Remove(manifest); err != nil {
				t.Fatalf("could not simulate completed registration removal: %v", err)
			}
			removeErr := errors.New("simulated package removal failure")
			var removed []string
			err := cleanupUninstalledDriverPackagesAfterRegistrationRemovalWithRemoveAll(cfg, info, func(path string) error {
				removed = append(removed, filepath.Clean(path))
				return removeErr
			})
			if !errors.Is(err, removeErr) {
				t.Fatalf("cleanup error = %v, want wrapped removal error", err)
			}
			if !slices.Contains(removed, filepath.Clean(directory)) {
				t.Fatalf("cleanup did not attempt removal of proven directory %q: %v", directory, removed)
			}
			if !strings.Contains(err.Error(), "driver registration was removed") {
				t.Fatalf("cleanup error = %v, want explicit registration-removed state", err)
			}
			if _, err := os.Stat(directory); err != nil {
				t.Fatalf("injected failure unexpectedly removed package directory: %v", err)
			}
			if _, err := os.Stat(manifest); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("runtime registration unexpectedly remains after cleanup failure: %v", err)
			}
		})
	}
}

func TestUninstallPackageCleanupSkipsNonDBCDrivers(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("managed library"))
	file := writeInstallArchive(t, archive, "managed")
	if _, err := InstallPackage(cfg, "example", file, installExpected("example", "source", archive), InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	info, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	managedDirectory := filepath.Dir(info.Driver.Shared.Get(PlatformTuple()))
	info.Source = "external"
	var removeCalls int
	err = cleanupUninstalledDriverPackagesWithRemoveAll(cfg, info, func(string) error {
		removeCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("non-dbc cleanup returned an error: %v", err)
	}
	if removeCalls != 0 {
		t.Fatalf("non-dbc cleanup called removeAll %d times, want 0", removeCalls)
	}
	if _, err := os.Stat(managedDirectory); err != nil {
		t.Fatalf("non-dbc cleanup removed a same-ID managed generation: %v", err)
	}

	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	info = DriverInfo{ID: "external", Source: "external"}
	if err := cleanupUninstalledDriverPackagesWithRemoveAll(Config{Level: ConfigEnv, Location: filePath}, info, func(string) error {
		removeCalls++
		return nil
	}); err != nil {
		t.Fatalf("non-dbc cleanup failed while config location was unreadable: %v", err)
	}
	if removeCalls != 0 {
		t.Fatalf("non-dbc cleanup called removeAll %d times for unreadable location, want 0", removeCalls)
	}
}

func TestUninstallDriverRetainsUnprovenLegacyDirectories(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, root string) (DriverInfo, string)
	}{
		{name: "unknown directory name", setup: func(t *testing.T, root string) (DriverInfo, string) {
			directory := filepath.Join(root, "example-unrecognized")
			if err := os.Mkdir(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			library := filepath.Join(directory, "old-library.so")
			if err := os.WriteFile(library, []byte("old library"), 0o644); err != nil {
				t.Fatal(err)
			}
			driver := DriverInfo{ID: "example", Name: "Example", Version: semver.MustParse("1.0.0"), Source: "dbc"}
			driver.Driver.Shared.defaultPath = library
			if err := CreateManifest(Config{Level: ConfigEnv, Location: root}, driver); err != nil {
				t.Fatal(err)
			}
			info, err := GetDriver(Config{Level: ConfigEnv, Location: root}, "example")
			if err != nil {
				t.Fatal(err)
			}
			return info, library
		}},
		{name: "corrupt receipt", setup: func(t *testing.T, root string) (DriverInfo, string) {
			legacy := createLegacyInstall(t, root, "example", "1.0.0", "")
			library := filepath.Join(legacy, "old-library.so")
			if err := os.WriteFile(filepath.Join(legacy, installReceiptName), []byte("broken"), 0o600); err != nil {
				t.Fatal(err)
			}
			info, err := GetDriver(Config{Level: ConfigEnv, Location: root}, "example")
			if err != nil {
				t.Fatal(err)
			}
			return info, library
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			info, library := test.setup(t, root)
			if err := UninstallDriver(Config{Level: ConfigEnv, Location: root}, info); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(library); err != nil || string(data) != "old library" {
				t.Fatalf("uninstall removed or changed unproven library: %q, %v", data, err)
			}
		})
	}
}

func TestRelativeNestedEnvironmentInstallUpdateUninstall(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	absRoot := filepath.Join(root, "nested", "drivers")
	relativeRoot, err := filepath.Rel(root, absRoot)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Level: ConfigEnv, Location: relativeRoot}

	firstArchive := makeInstallArchive(t, "example", "1.0.0", "first.so", []byte("first library"))
	firstFile := writeInstallArchive(t, firstArchive, "relative-first")
	first, err := InstallPackage(cfg, "example", firstFile, installExpected("example", "relative-first", firstArchive), InstallOptions{})
	_ = firstFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	firstGeneration := filepath.Dir(first.Driver.Shared.Get(PlatformTuple()))
	if _, err := os.Stat(filepath.Join(absRoot, "example.toml")); err != nil {
		t.Fatalf("runtime manifest missing after first install: %v", err)
	}
	link := filepath.Join(filepath.Dir(absRoot), "example.toml")
	linkTarget, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("manifest compatibility symlink missing after first install: %v", err)
	}
	if !filepath.IsAbs(linkTarget) {
		linkTarget = filepath.Join(filepath.Dir(absRoot), linkTarget)
	}
	resolvedLinkTarget, err := filepath.Abs(linkTarget)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath, err := filepath.Abs(filepath.Join(absRoot, "example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(resolvedLinkTarget) != filepath.Clean(manifestPath) {
		t.Fatalf("manifest symlink resolves to %q, want %q", resolvedLinkTarget, manifestPath)
	}

	if err := installVersionTwo(t, cfg, "example", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(firstGeneration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old owned generation remains after update: %v", err)
	}
	secondInfo, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	secondGeneration := filepath.Dir(secondInfo.Driver.Shared.Get(PlatformTuple()))
	if _, err := os.Stat(secondGeneration); err != nil {
		t.Fatalf("new owned generation missing after update: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("manifest symlink missing after update: %v", err)
	}

	if err := UninstallDriver(cfg, secondInfo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(absRoot, "example.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime manifest remains after uninstall: %v", err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest symlink remains after uninstall: %v", err)
	}
	if _, err := os.Stat(secondGeneration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned generation remains after uninstall: %v", err)
	}
	if got := countInstallGenerations(t, absRoot, "example"); got != 0 {
		t.Fatalf("owned package generation count after uninstall = %d, want 0", got)
	}
}

func installArchiveBasenamePackage(t *testing.T, root string) (Manifest, string) {
	t.Helper()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("installed library"))
	file := writeInstallArchive(t, archive, "release-bundle")
	installed, err := InstallDriver(cfg, "example", file)
	if err != nil {
		t.Fatal(err)
	}
	packageDirectory := filepath.Dir(installed.Driver.Shared.Get(PlatformTuple()))
	if err := CreateManifest(cfg, installed.DriverInfo); err != nil {
		t.Fatal(err)
	}
	return installed, packageDirectory
}

func TestUninstallDriverUsesReceiptForArchiveBasenameDirectory(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	_, packageDirectory := installArchiveBasenamePackage(t, root)
	base := filepath.Base(packageDirectory)
	if strings.HasPrefix(base, ".dbc-package-example-") || base == "example_"+PlatformTuple()+"_v1.0.0" {
		t.Fatalf("test package directory unexpectedly uses a managed name: %q", base)
	}
	info, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := managedPackageDirectory(root, "example", info); !ok || filepath.Clean(got) != filepath.Clean(packageDirectory) {
		t.Fatalf("registered shared path did not prove receipt-owned directory: %q, %v", got, ok)
	}
	if err := UninstallDriver(cfg, info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(packageDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("receipt-owned archive basename directory remains after uninstall: %v", err)
	}
}

func TestUninstallDriverRetainsInvalidReceiptAtArchiveBasenameDirectory(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installed, packageDirectory := installArchiveBasenamePackage(t, root)
	if err := os.WriteFile(filepath.Join(packageDirectory, installReceiptName), []byte("broken receipt"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := managedPackageDirectory(root, "example", info); ok {
		t.Fatal("invalid receipt proved ownership of an archive basename directory")
	}
	if err := UninstallDriver(cfg, info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(packageDirectory); err != nil {
		t.Fatalf("invalid receipt directory was removed: %v", err)
	}
	if data, err := os.ReadFile(installed.Driver.Shared.Get(PlatformTuple())); err != nil || string(data) != "installed library" {
		t.Fatalf("invalid receipt library was removed or changed: %q, %v", data, err)
	}
}

func TestManagedPackageDirectoryRequiresReceiptLibraryRelationship(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("driver"))
	file := writeInstallArchive(t, archive, "initial")
	installed, err := InstallPackage(cfg, "example", file, installExpected("example", "registry-source", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	dir, ok := managedPackageDirectory(root, "example", installed.DriverInfo)
	if !ok {
		t.Fatal("valid receipt did not prove package ownership")
	}
	registryInfo := installed.DriverInfo
	registryInfo.Driver.Shared = driverMap{defaultPath: installed.Driver.Shared.Get(PlatformTuple())}
	if _, ok := managedPackageDirectory(root, "example", registryInfo); !ok {
		t.Fatal("valid managed directory was not recognized through a default shared path")
	}
	wrongID := installed.DriverInfo
	wrongID.ID = "another-driver"
	if _, ok := managedPackageDirectory(root, "example", wrongID); ok {
		t.Fatal("package receipt for another driver proved ownership")
	}
	linkDir := filepath.Join(root, ".dbc-package-example-link")
	if err := os.Symlink(dir, linkDir); err == nil {
		linkedInfo := installed.DriverInfo
		linkedInfo.Driver.Shared.platformMap = map[string]string{
			PlatformTuple(): filepath.Join(linkDir, filepath.Base(installed.Driver.Shared.Get(PlatformTuple()))),
		}
		if _, ok := managedPackageDirectory(root, "example", linkedInfo); ok {
			t.Fatal("symlinked package directory proved ownership")
		}
	}
	receiptPath := filepath.Join(dir, installReceiptName)
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt InstallReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.InstalledLibraryHash = "sha256:" + strings.Repeat("0", 64)
	data, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := managedPackageDirectory(root, "example", installed.DriverInfo); ok {
		t.Fatal("receipt with a mismatched library digest proved ownership")
	}
}

func TestCleanupManagedPackageDirectoriesSkipsForeignOrTamperedGenerations(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	exampleArchive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("example"))
	exampleFile := writeInstallArchive(t, exampleArchive, "example")
	example, err := InstallPackage(cfg, "example", exampleFile, installExpected("example", "example-source", exampleArchive), InstallOptions{})
	_ = exampleFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	otherArchive := makeInstallArchive(t, "other", "1.0.0", "driver.so", []byte("other"))
	otherFile := writeInstallArchive(t, otherArchive, "other")
	other, err := InstallPackage(cfg, "other", otherFile, installExpected("other", "other-source", otherArchive), InstallOptions{})
	_ = otherFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	exampleDir := filepath.Dir(example.Driver.Shared.Get(PlatformTuple()))
	otherDir := filepath.Dir(other.Driver.Shared.Get(PlatformTuple()))

	tamperedDir := filepath.Join(root, ".dbc-package-example-tampered")
	if err := os.Mkdir(tamperedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	receiptBytes, err := os.ReadFile(filepath.Join(exampleDir, installReceiptName))
	if err != nil {
		t.Fatal(err)
	}
	var tamperedReceipt InstallReceipt
	if err := json.Unmarshal(receiptBytes, &tamperedReceipt); err != nil {
		t.Fatal(err)
	}
	tamperedReceipt.SourceIdentity = ""
	receiptBytes, err = json.Marshal(tamperedReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tamperedDir, installReceiptName), receiptBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tamperedDir, "driver.so"), []byte("example"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkDir := filepath.Join(root, ".dbc-package-example-symlink")
	symlinkCreated := os.Symlink(exampleDir, symlinkDir) == nil

	newDir := filepath.Join(root, ".dbc-package-example-current")
	referenced := DriverInfo{ID: "example", Source: "dbc", Version: example.Version}
	referenced.Driver.Shared.defaultPath = example.Driver.Shared.Get(PlatformTuple())
	cleanupManagedPackageDirectories(root, "example", newDir, referenced)
	if _, err := os.Stat(exampleDir); err != nil {
		t.Fatalf("generation referenced by current runtime manifest was removed: %v", err)
	}
	current := DriverInfo{ID: "example", Source: "dbc", Version: example.Version}
	current.Driver.Shared.defaultPath = filepath.Join(root, "external.so")
	cleanupManagedPackageDirectories(root, "example", newDir, current)
	if _, err := os.Stat(exampleDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("valid old generation was not cleaned: %v", err)
	}
	if _, err := os.Stat(tamperedDir); err != nil {
		t.Fatalf("generation with tampered archive metadata was removed: %v", err)
	}
	if symlinkCreated {
		if _, err := os.Lstat(symlinkDir); err != nil {
			t.Fatalf("symlink generation was removed: %v", err)
		}
	}
	if _, err := os.Stat(otherDir); err != nil {
		t.Fatalf("another driver's generation was removed: %v", err)
	}
}
