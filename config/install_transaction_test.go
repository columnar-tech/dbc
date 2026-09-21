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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
)

type installArchiveEntry struct {
	name string
	data []byte
}

func makeInstallArchive(t *testing.T, id, version, library string, libraryData []byte) []byte {
	t.Helper()
	manifest := []byte(fmt.Sprintf(`package_version = 2
id = %q
name = "Example Driver"
version = %q
platform = %q

[Driver]
entrypoint = "AdbcDriverExampleInit"

[Files]
driver = %q
`, id, version, PlatformTuple(), library))
	return makeInstallArchiveWithEntries(t,
		installArchiveEntry{name: "dbc-package.toml", data: manifest},
		installArchiveEntry{name: library, data: libraryData},
	)
}

func makeInstallArchiveWithEntries(t *testing.T, entries ...installArchiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		err := writer.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.data)), Typeflag: tar.TypeReg, Format: tar.FormatPAX})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writeInstallArchive(t *testing.T, data []byte, name string) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), name+"-*.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	return file
}

func installExpected(id, source string, data []byte) ExpectedPackageMetadata {
	digest := sha256.Sum256(data)
	return ExpectedPackageMetadata{
		ID: id, Version: "1.0.0", Platform: PlatformTuple(),
		SourceType: "registry", SourceIdentity: source,
		ArchiveHash: "sha256:" + hex.EncodeToString(digest[:]), ArchiveSize: int64(len(data)),
	}
}

func TestInstallPackageVerifiesBeforeReplacingRuntimeRegistration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	firstArchive := makeInstallArchive(t, "example", "1.0.0", "old-library.so", []byte("old library"))
	firstFile := writeInstallArchive(t, firstArchive, "first")
	first, err := InstallPackage(cfg, "example", firstFile, installExpected("example", "source-one", firstArchive), InstallOptions{})
	_ = firstFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	oldPath := first.Driver.Shared.Get(PlatformTuple())
	oldManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	if err != nil {
		t.Fatal(err)
	}

	secondArchive := makeInstallArchive(t, "example", "1.0.0", "new-library.so", []byte("new library"))
	secondFile := writeInstallArchive(t, secondArchive, "second")
	verifyErr := errors.New("signature rejected")
	_, err = InstallPackage(cfg, "example", secondFile, installExpected("example", "source-two", secondArchive), InstallOptions{
		Verify: func(stagingDir string, manifest Manifest) error {
			if got, readErr := os.ReadFile(oldPath); readErr != nil || string(got) != "old library" {
				t.Fatalf("old library unavailable during verification: %q, %v", got, readErr)
			}
			if got, readErr := os.ReadFile(filepath.Join(root, "example.toml")); readErr != nil || !bytes.Equal(got, oldManifest) {
				t.Fatalf("old runtime manifest changed before verification completed: %v", readErr)
			}
			if _, readErr := os.Stat(filepath.Join(stagingDir, manifest.Files.Driver)); readErr != nil {
				t.Fatalf("verified library is not staged: %v", readErr)
			}
			return verifyErr
		},
	})
	_ = secondFile.Close()
	if !errors.Is(err, verifyErr) {
		t.Fatalf("InstallPackage error = %v, want verifier error", err)
	}
	currentManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	if err != nil || !bytes.Equal(currentManifest, oldManifest) {
		t.Fatalf("runtime manifest changed after verification failure: %v", err)
	}
	if got, err := os.ReadFile(oldPath); err != nil || string(got) != "old library" {
		t.Fatalf("old library unavailable after verification failure: %q, %v", got, err)
	}
}

func TestInstallPackageMetadataFailuresPreserveOldGeneration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	firstArchive := makeInstallArchive(t, "example", "1.0.0", "old-library.so", []byte("old library"))
	firstFile := writeInstallArchive(t, firstArchive, "initial")
	first, err := InstallPackage(cfg, "example", firstFile, installExpected("example", "source-one", firstArchive), InstallOptions{})
	_ = firstFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	oldManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	oldLibrary, err := os.ReadFile(first.Driver.Shared.Get(PlatformTuple()))
	if err != nil {
		t.Fatal(err)
	}

	archive := makeInstallArchive(t, "example", "1.0.0", "replacement.so", []byte("new library"))
	tests := []struct {
		name   string
		mutate func(*ExpectedPackageMetadata)
	}{
		{name: "archive digest mismatch", mutate: func(expected *ExpectedPackageMetadata) {
			expected.ArchiveHash = "sha256:" + strings.Repeat("0", 64)
		}},
		{name: "package version mismatch", mutate: func(expected *ExpectedPackageMetadata) {
			expected.Version = "2.0.0"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := installExpected("example", "source-two", archive)
			test.mutate(&expected)
			file := writeInstallArchive(t, archive, test.name)
			_, err := InstallPackage(cfg, "example", file, expected, InstallOptions{})
			_ = file.Close()
			if err == nil {
				t.Fatal("InstallPackage unexpectedly succeeded")
			}
			currentManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
			if err != nil || !bytes.Equal(currentManifest, oldManifest) {
				t.Fatalf("runtime manifest changed after metadata failure: %v", err)
			}
			currentLibrary, err := os.ReadFile(first.Driver.Shared.Get(PlatformTuple()))
			if err != nil || !bytes.Equal(currentLibrary, oldLibrary) {
				t.Fatalf("old library changed after metadata failure: %v", err)
			}
		})
	}
}

func TestInstallPackageReceiptHashesLibraryAfterVerification(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("archive library"))
	file := writeInstallArchive(t, archive, "rewrite")
	rewritten := []byte("verified replacement library")
	installed, err := InstallPackage(cfg, "example", file, installExpected("example", "rewrite-source", archive), InstallOptions{
		Verify: func(stagingDir string, manifest Manifest) error {
			return os.WriteFile(filepath.Join(stagingDir, manifest.Files.Driver), rewritten, 0o644)
		},
	})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	libraryPath := installed.Driver.Shared.Get(PlatformTuple())
	gotLibrary, err := os.ReadFile(libraryPath)
	if err != nil || !bytes.Equal(gotLibrary, rewritten) {
		t.Fatalf("installed library = %q, want verifier output %q: %v", gotLibrary, rewritten, err)
	}
	receiptData, err := os.ReadFile(filepath.Join(filepath.Dir(libraryPath), installReceiptName))
	if err != nil {
		t.Fatal(err)
	}
	var receipt InstallReceipt
	if err := json.Unmarshal(receiptData, &receipt); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(rewritten)
	wantHash := "sha256:" + hex.EncodeToString(digest[:])
	if receipt.InstalledLibraryHash != wantHash {
		t.Fatalf("receipt library hash = %q, want %q", receipt.InstalledLibraryHash, wantHash)
	}
}

func TestInspectInstallReceiptSeparatesMetadataFromLibraryIntegrity(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("library"))
	file := writeInstallArchive(t, archive, "receipt-inspection")
	installed, err := InstallPackage(cfg, "example", file, installExpected("example", "receipt-source", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	libraryPath := installed.Driver.Shared.Get(PlatformTuple())
	receipt, managed, present, valid := InspectInstallReceipt(root, "example", libraryPath)
	if !managed || !present || !valid {
		t.Fatalf("receipt inspection = managed %v, present %v, valid %v; want all true", managed, present, valid)
	}
	if !VerifyInstallReceiptLibraryIntegrity(libraryPath, receipt) {
		t.Fatal("installed library should match its receipt")
	}

	if err := os.WriteFile(libraryPath, []byte("tampered library"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, managed, present, valid = InspectInstallReceipt(root, "example", libraryPath)
	if !managed || !present || !valid {
		t.Fatalf("metadata inspection should not hash current library: managed %v, present %v, valid %v", managed, present, valid)
	}
	if VerifyInstallReceiptLibraryIntegrity(libraryPath, receipt) {
		t.Fatal("tampered library unexpectedly matched its receipt")
	}

	if err := os.Remove(filepath.Join(filepath.Dir(libraryPath), installReceiptName)); err != nil {
		t.Fatal(err)
	}
	_, managed, present, valid = InspectInstallReceipt(root, "example", libraryPath)
	if !managed || present || valid {
		t.Fatalf("missing receipt inspection = managed %v, present %v, valid %v; want true, false, false", managed, present, valid)
	}

	legacyDirectory := filepath.Join(root, "example_"+PlatformTuple()+"_v1.0.0")
	if err := os.Mkdir(legacyDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyLibrary := filepath.Join(legacyDirectory, "driver.so")
	if err := os.WriteFile(legacyLibrary, []byte("legacy library"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, managed, present, valid = InspectInstallReceipt(root, "example", legacyLibrary)
	if !managed || present || valid {
		t.Fatalf("legacy receipt-less generation = managed %v, present %v, valid %v; want true, false, false", managed, present, valid)
	}
	externalDirectory := filepath.Join(root, "external")
	if err := os.Mkdir(externalDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	externalLibrary := filepath.Join(externalDirectory, "driver.so")
	if err := os.WriteFile(externalLibrary, []byte("external library"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, managed, present, valid = InspectInstallReceipt(root, "example", externalLibrary)
	if managed || present || valid {
		t.Fatalf("external library = managed %v, present %v, valid %v; want all false", managed, present, valid)
	}
}

func TestInspectDriverInstallReceiptUsesSelectedEnvironmentPath(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("library"))
	file := writeInstallArchive(t, archive, "multi-path-receipt")
	installed, err := InstallPackage(Config{Level: ConfigEnv, Location: second}, "example", file, installExpected("example", "multi-path", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	combined := Config{Level: ConfigEnv, Location: first + string(filepath.ListSeparator) + second}
	registered, err := GetDriver(combined, "example")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(registered.FilePath) != filepath.Clean(second) {
		t.Fatalf("selected registration root = %q, want %q", registered.FilePath, second)
	}
	receipt, managed, present, valid, err := InspectDriverInstallReceipt(combined, registered)
	if err != nil {
		t.Fatal(err)
	}
	if !managed || !present || !valid || !VerifyInstallReceiptLibraryIntegrity(installed.Driver.Shared.Get(PlatformTuple()), receipt) {
		t.Fatalf("multi-path receipt inspection = managed %v present %v valid %v", managed, present, valid)
	}
	registryMapped := registered
	registryMapped.FilePath = "HKCU\\SOFTWARE\\ADBC\\Drivers"
	receipt, managed, present, valid, err = InspectDriverInstallReceipt(Config{Level: ConfigUser, Location: second}, registryMapped)
	if err != nil {
		t.Fatal(err)
	}
	if !managed || !present || !valid {
		t.Fatalf("registry-backed receipt inspection = managed %v present %v valid %v", managed, present, valid)
	}
}

func TestValidatePackageReturnsAndComparesRuntimeRegistration(t *testing.T) {
	root := t.TempDir()
	externalLibrary := filepath.Join(root, "external-driver.so")
	if err := os.WriteFile(externalLibrary, []byte("external library"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(fmt.Sprintf(`manifest_version = 1
name = "Manifest Only Driver"
version = "1.0.0"

[Driver]
entrypoint = "DriverInit"
shared = %q
`, externalLibrary))
	archive := makeInstallArchiveWithEntries(t, installArchiveEntry{name: "MANIFEST", data: manifest})
	file := writeInstallArchive(t, archive, "external-registration")
	validation, err := ValidatePackage("example", file, installExpected("example", "source", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if validation.VerifiedLibraryHash != "" {
		t.Fatalf("manifest-only package library hash = %q, want empty", validation.VerifiedLibraryHash)
	}
	candidate := validation.Registration
	if candidate.ID != "example" || candidate.Driver.Shared.Get(PlatformTuple()) != externalLibrary || candidate.Driver.Entrypoint != "DriverInit" {
		t.Fatalf("validated registration = %#v, want external path %q and entrypoint DriverInit", candidate, externalLibrary)
	}
	current := candidate
	current.FilePath = filepath.Join(root, "registration")
	if !SameRuntimeDriverRegistration(current, candidate, PlatformTuple()) {
		t.Fatal("registration location alone should not change effective runtime registration")
	}
	changedPath := DriverInfo{
		ID: candidate.ID, Name: candidate.Name, Publisher: candidate.Publisher, License: candidate.License,
		Version: candidate.Version, Source: candidate.Source, AdbcInfo: candidate.AdbcInfo,
	}
	changedPath.Driver.Entrypoint = candidate.Driver.Entrypoint
	changedPath.Driver.Shared.Set(PlatformTuple(), filepath.Join(root, "other-driver.so"))
	if SameRuntimeDriverRegistration(current, changedPath, PlatformTuple()) {
		t.Fatal("different external library paths unexpectedly compare equal")
	}
	changedEntrypoint := candidate
	changedEntrypoint.Driver.Entrypoint = "OtherInit"
	if SameRuntimeDriverRegistration(current, changedEntrypoint, PlatformTuple()) {
		t.Fatal("different entrypoints unexpectedly compare equal")
	}
	changedVersion := candidate
	changedVersion.Version = semver.MustParse("1.0.1")
	if SameRuntimeDriverRegistration(current, changedVersion, PlatformTuple()) {
		t.Fatal("different manifest versions unexpectedly compare equal")
	}
}

func TestInstallPackageRejectsVerifierDeletedLibraryAndPreservesOldGeneration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	firstArchive := makeInstallArchive(t, "example", "1.0.0", "old-library.so", []byte("old library"))
	firstFile := writeInstallArchive(t, firstArchive, "initial")
	first, err := InstallPackage(cfg, "example", firstFile, installExpected("example", "source-one", firstArchive), InstallOptions{})
	_ = firstFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	oldPath := first.Driver.Shared.Get(PlatformTuple())
	oldManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	secondArchive := makeInstallArchive(t, "example", "1.0.0", "new-library.so", []byte("new library"))
	secondFile := writeInstallArchive(t, secondArchive, "delete")
	_, err = InstallPackage(cfg, "example", secondFile, installExpected("example", "source-two", secondArchive), InstallOptions{
		Verify: func(stagingDir string, manifest Manifest) error {
			return os.Remove(filepath.Join(stagingDir, manifest.Files.Driver))
		},
	})
	_ = secondFile.Close()
	if err == nil {
		t.Fatal("InstallPackage succeeded after verifier deleted the driver library")
	}
	currentManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	if err != nil || !bytes.Equal(currentManifest, oldManifest) {
		t.Fatalf("runtime manifest changed after verifier deleted the staged library: %v", err)
	}
	if got, err := os.ReadFile(oldPath); err != nil || string(got) != "old library" {
		t.Fatalf("old library unavailable after verifier deleted the staged library: %q, %v", got, err)
	}
}

func TestInstallPackageRegistrationFailurePreservesOldGeneration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	firstArchive := makeInstallArchive(t, "example", "1.0.0", "old-library.so", []byte("old library"))
	firstFile := writeInstallArchive(t, firstArchive, "initial")
	first, err := InstallPackage(cfg, "example", firstFile, installExpected("example", "source-one", firstArchive), InstallOptions{})
	_ = firstFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	oldPath := first.Driver.Shared.Get(PlatformTuple())
	oldDir := filepath.Dir(oldPath)
	oldManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	secondArchive := makeInstallArchive(t, "example", "1.0.0", "renamed-library.so", []byte("new library"))
	secondFile := writeInstallArchive(t, secondArchive, "replacement")
	registerErr := errors.New("runtime registration failed")
	_, err = installPackage(cfg, "example", secondFile, installExpected("example", "source-two", secondArchive), InstallOptions{}, func(gotCfg Config, _ DriverInfo) error {
		current, loadErr := loadDriverFromManifest(root, "example")
		if loadErr != nil {
			t.Fatalf("old runtime manifest unavailable before registration: %v", loadErr)
		}
		if current.Driver.Shared.Get(PlatformTuple()) != oldPath {
			t.Fatalf("old runtime path changed before registration: %q", current.Driver.Shared.Get(PlatformTuple()))
		}
		if got, readErr := os.ReadFile(oldPath); readErr != nil || string(got) != "old library" {
			t.Fatalf("old library unavailable before registration: %q, %v", got, readErr)
		}
		return registerErr
	})
	_ = secondFile.Close()
	if !errors.Is(err, registerErr) {
		t.Fatalf("install error = %v, want registration error", err)
	}
	currentManifest, err := os.ReadFile(filepath.Join(root, "example.toml"))
	if err != nil || !bytes.Equal(currentManifest, oldManifest) {
		t.Fatalf("runtime manifest changed after failed registration: %v", err)
	}
	if got, err := os.ReadFile(oldPath); err != nil || string(got) != "old library" {
		t.Fatalf("old library unavailable after failed registration: %q, %v", got, err)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("old package generation was removed after failed registration: %v", err)
	}
	if got := countInstallGenerations(t, root, "example"); got != 1 {
		t.Fatalf("generation count after failed registration = %d, want only old generation", got)
	}
}

func TestInstallPackagePreservesCandidateWhenRegistrationRollbackFails(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	firstArchive := makeInstallArchive(t, "example", "1.0.0", "old-library.so", []byte("old library"))
	firstFile := writeInstallArchive(t, firstArchive, "initial")
	first, err := InstallPackage(cfg, "example", firstFile, installExpected("example", "source-one", firstArchive), InstallOptions{})
	_ = firstFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	secondArchive := makeInstallArchive(t, "example", "1.0.0", "new-library.so", []byte("new library"))
	secondFile := writeInstallArchive(t, secondArchive, "replacement")
	writeErr := errors.New("registration failed")
	rollbackErr := errors.New("registration rollback failed")
	_, err = installPackage(cfg, "example", secondFile, installExpected("example", "source-two", secondArchive), InstallOptions{}, func(Config, DriverInfo) error {
		return &manifestRollbackError{writeErr: writeErr, rollbackErr: rollbackErr}
	})
	_ = secondFile.Close()
	if !errors.Is(err, writeErr) || !strings.Contains(err.Error(), rollbackErr.Error()) {
		t.Fatalf("install error = %v, want registration and rollback errors", err)
	}
	if got := countInstallGenerations(t, root, "example"); got != 2 {
		t.Fatalf("generation count after failed rollback = %d, want old and preserved candidate", got)
	}
	current, err := loadDriverFromManifest(root, "example")
	if err != nil || current.Driver.Shared.Get(PlatformTuple()) != first.Driver.Shared.Get(PlatformTuple()) {
		t.Fatalf("old runtime manifest changed after rollback failure: %v", err)
	}
}

func TestInstallPackageCleansOldGenerationOnlyAfterRegistration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	firstArchive := makeInstallArchive(t, "example", "1.0.0", "old-library.so", []byte("old library"))
	firstFile := writeInstallArchive(t, firstArchive, "initial")
	first, err := InstallPackage(cfg, "example", firstFile, installExpected("example", "source-one", firstArchive), InstallOptions{})
	_ = firstFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Dir(first.Driver.Shared.Get(PlatformTuple()))
	secondArchive := makeInstallArchive(t, "example", "1.0.0", "new-library.so", []byte("new library"))
	secondFile := writeInstallArchive(t, secondArchive, "replacement")
	second, err := InstallPackage(cfg, "example", secondFile, installExpected("example", "source-two", secondArchive), InstallOptions{})
	_ = secondFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old package generation remains after successful registration: %v", err)
	}
	if got, err := os.ReadFile(second.Driver.Shared.Get(PlatformTuple())); err != nil || string(got) != "new library" {
		t.Fatalf("new package generation unavailable: %q, %v", got, err)
	}
}

func TestInstallPackageIgnoresOldGenerationCleanupFailure(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	firstArchive := makeInstallArchive(t, "example", "1.0.0", "old-library.so", []byte("old library"))
	firstFile := writeInstallArchive(t, firstArchive, "initial")
	first, err := InstallPackage(cfg, "example", firstFile, installExpected("example", "source-one", firstArchive), InstallOptions{})
	_ = firstFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Dir(first.Driver.Shared.Get(PlatformTuple()))
	secondArchive := makeInstallArchive(t, "example", "1.0.0", "new-library.so", []byte("new library"))
	secondFile := writeInstallArchive(t, secondArchive, "replacement")
	cleanupErr := errors.New("package removal failed")
	installed, err := installPackageWithCleanup(cfg, "example", secondFile, installExpected("example", "source-two", secondArchive), InstallOptions{}, CreateManifest, func(string, string, *DriverInfo, string, DriverInfo) error {
		return cleanupErr
	})
	_ = secondFile.Close()
	if err != nil {
		t.Fatalf("install returned stale-generation cleanup error: %v", err)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("old generation missing after best-effort cleanup failure: %v", err)
	}
	current, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	if current.Driver.Shared.Get(PlatformTuple()) != installed.Driver.Shared.Get(PlatformTuple()) {
		t.Fatalf("runtime registration path = %q, want installed path %q", current.Driver.Shared.Get(PlatformTuple()), installed.Driver.Shared.Get(PlatformTuple()))
	}
}

func TestInstallPackageConcurrentGenerationsShareDriverLock(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archives := [][]byte{
		makeInstallArchive(t, "example", "1.0.0", "first-library.so", []byte("first")),
		makeInstallArchive(t, "example", "1.0.0", "second-library.so", []byte("second")),
	}
	results := make(chan error, len(archives))
	var wait sync.WaitGroup
	for i, archive := range archives {
		wait.Add(1)
		go func(i int, archive []byte) {
			defer wait.Done()
			file, err := os.CreateTemp(t.TempDir(), fmt.Sprintf("source-%d-*.tar.gz", i))
			if err == nil {
				_, err = file.Write(archive)
			}
			if err == nil {
				_, err = file.Seek(0, io.SeekStart)
			}
			if err == nil {
				_, err = InstallPackage(cfg, "example", file, installExpected("example", fmt.Sprintf("source-%d", i), archive), InstallOptions{})
			}
			if file != nil {
				_ = file.Close()
			}
			results <- err
		}(i, archive)
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	installed, err := loadDriverFromManifest(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(installed.Driver.Shared.Get(PlatformTuple()))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first" && string(data) != "second" {
		t.Fatalf("installed library = %q", data)
	}
	var receipt InstallReceipt
	receiptData, err := os.ReadFile(filepath.Join(filepath.Dir(installed.Driver.Shared.Get(PlatformTuple())), installReceiptName))
	if err != nil || json.Unmarshal(receiptData, &receipt) != nil {
		t.Fatalf("could not read installed generation receipt: %v", err)
	}
	if receipt.InstalledLibrary != filepath.Base(installed.Driver.Shared.Get(PlatformTuple())) {
		t.Fatalf("receipt library = %q, runtime path = %q", receipt.InstalledLibrary, installed.Driver.Shared.Get(PlatformTuple()))
	}
	actualLibraryHash, err := hashFile(installed.Driver.Shared.Get(PlatformTuple()))
	if err != nil || receipt.InstalledLibraryHash != actualLibraryHash {
		t.Fatalf("receipt library digest %q does not match installed library %q: %v", receipt.InstalledLibraryHash, actualLibraryHash, err)
	}
	if (receipt.SourceIdentity == "source-0" && string(data) != "first") || (receipt.SourceIdentity == "source-1" && string(data) != "second") {
		t.Fatalf("receipt source %q does not match installed library %q", receipt.SourceIdentity, data)
	}
}

func TestUninstallDriverWaitsForInstallLock(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := makeInstallArchive(t, "example", "1.0.0", "library.so", []byte("installed library"))
	file := writeInstallArchive(t, archive, "uninstall-lock")
	installed, err := InstallPackage(cfg, "example", file, installExpected("example", "uninstall-lock", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	info, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	libraryPath := installed.Driver.Shared.Get(PlatformTuple())
	if data, err := os.ReadFile(libraryPath); err != nil || string(data) != "installed library" {
		t.Fatalf("installed library unavailable before uninstall: %q, %v", data, err)
	}
	location, err := driverInstallLockLocation(cfg, info)
	if err != nil {
		t.Fatal(err)
	}
	releaseLock, err := acquireDriverInstallLock(location, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			releaseLock()
		}
	}()

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- UninstallDriver(cfg, info)
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("uninstall completed while the install lock was held: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	_, manifestErr := os.Stat(filepath.Join(root, "example.toml"))
	libraryData, libraryErr := os.ReadFile(libraryPath)
	releaseLock()
	lockHeld = false
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UninstallDriver returned an error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UninstallDriver did not complete after the install lock was released")
	}
	if manifestErr != nil {
		t.Fatalf("runtime manifest changed while the install lock was held: %v", manifestErr)
	}
	if libraryErr != nil || string(libraryData) != "installed library" {
		t.Fatalf("installed library changed while the install lock was held: %q, %v", libraryData, libraryErr)
	}
	if _, err := os.Stat(filepath.Join(root, "example.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime manifest still exists after uninstall: %v", err)
	}
	if _, err := os.Stat(libraryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installed library still exists after uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(libraryPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("receipt-owned generation directory remains after uninstall: %v", err)
	}
}

func TestDriverInstallLockLocation(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	registryPath := filepath.Join(root, "registry-fallback")
	for _, location := range []string{first, second, registryPath} {
		if err := os.MkdirAll(location, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		cfg  Config
		info DriverInfo
		want string
	}{
		{
			name: "environment uses actual registration location",
			cfg:  Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second},
			info: DriverInfo{ID: "example", FilePath: second},
			want: second,
		},
		{
			name: "filesystem manifest uses its actual location",
			cfg:  Config{Level: ConfigUser, Location: first},
			info: DriverInfo{ID: "example", FilePath: second},
			want: second,
		},
		{
			name: "registry manifest uses configured filesystem root",
			cfg:  Config{Level: ConfigUser, Location: registryPath},
			info: DriverInfo{ID: "example", FilePath: `HKCU\\SOFTWARE\\ADBC\\Drivers`},
			want: registryPath,
		},
		{
			name: "registry manifest defaults to config filesystem root",
			cfg:  Config{Level: ConfigUser},
			info: DriverInfo{ID: "example", FilePath: `HKCU\\SOFTWARE\\ADBC\\Drivers`},
			want: ConfigUser.ConfigLocation(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := driverInstallLockLocation(test.cfg, test.info)
			if err != nil {
				t.Fatal(err)
			}
			want, err := filepath.Abs(test.want)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("driverInstallLockLocation() = %q, want %q", got, want)
			}
		})
	}
}

func TestUninstallPackageCleanupLocation(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	registryPath := filepath.Join(root, "registry-fallback")
	for _, location := range []string{first, second, registryPath} {
		if err := os.MkdirAll(location, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		cfg  Config
		info DriverInfo
		want string
	}{
		{
			name: "environment cleans the registered manifest directory",
			cfg:  Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second},
			info: DriverInfo{ID: "example", FilePath: second},
			want: second,
		},
		{
			name: "filesystem manifest cleans its actual directory",
			cfg:  Config{Level: ConfigUser, Location: first},
			info: DriverInfo{ID: "example", FilePath: second},
			want: second,
		},
		{
			name: "registry manifest falls back to configured filesystem root",
			cfg:  Config{Level: ConfigUser, Location: registryPath},
			info: DriverInfo{ID: "example", FilePath: `HKCU\\SOFTWARE\\ADBC\\Drivers`},
			want: registryPath,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := uninstallPackageCleanupLocation(test.cfg, test.info)
			if err != nil {
				t.Fatal(err)
			}
			want, err := filepath.Abs(test.want)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("uninstallPackageCleanupLocation() = %q, want %q", got, want)
			}
		})
	}
}

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

func countInstallGenerations(t *testing.T, location, runtimeID string) int {
	t.Helper()
	entries, err := os.ReadDir(location)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".dbc-package-"+runtimeID+"-") {
			count++
		}
	}
	return count
}

func createLegacyInstall(t *testing.T, location, runtimeID, version string, sharedPath string) string {
	t.Helper()
	directory := filepath.Join(location, runtimeID+"_"+PlatformTuple()+"_v"+version)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if sharedPath == "" {
		sharedPath = filepath.Join(directory, "old-library.so")
	}
	if err := os.WriteFile(sharedPath, []byte("old library"), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := DriverInfo{
		ID: runtimeID, Name: "Example Driver", Version: semver.MustParse(version), Source: "dbc",
	}
	driver.Driver.Shared.defaultPath = sharedPath
	if err := CreateManifest(Config{Level: ConfigEnv, Location: location}, driver); err != nil {
		t.Fatal(err)
	}
	return directory
}

func installVersionTwo(t *testing.T, cfg Config, runtimeID string, verify func(string, Manifest) error, register func(Config, DriverInfo) error) error {
	t.Helper()
	archive := makeInstallArchive(t, runtimeID, "2.0.0", "new-library.so", []byte("new library"))
	file := writeInstallArchive(t, archive, "version-two")
	defer file.Close()
	expected := installExpected(runtimeID, "new-source", archive)
	expected.Version = "2.0.0"
	if register == nil {
		_, err := InstallPackage(cfg, runtimeID, file, expected, InstallOptions{Verify: verify})
		return err
	}
	_, err := installPackage(cfg, runtimeID, file, expected, InstallOptions{Verify: verify}, register)
	return err
}

func TestUninstallDriverUsesRegisteredEnvironmentPath(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	registered := filepath.Join(root, "registered")
	if err := os.Mkdir(registered, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := makeInstallArchive(t, "example", "1.0.0", "library.so", []byte("installed library"))
	file := writeInstallArchive(t, archive, "registered")
	_, err := InstallPackage(Config{Level: ConfigEnv, Location: registered}, "example", file, installExpected("example", "registered", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	info, err := loadDriverFromManifest(registered, "example")
	if err != nil {
		t.Fatal(err)
	}
	combined := Config{Level: ConfigEnv, Location: missing + string(os.PathListSeparator) + registered}
	lockLocation, err := driverInstallLockLocation(combined, info)
	if err != nil {
		t.Fatal(err)
	}
	wantLockLocation, _ := filepath.Abs(registered)
	if lockLocation != wantLockLocation {
		t.Fatalf("uninstall lock location = %q, want actual registration location %q", lockLocation, wantLockLocation)
	}
	if err := UninstallDriver(combined, info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall created the missing first path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(registered, "example.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registered manifest still exists: %v", err)
	}
}

func TestUninstallDriverEnvironmentPathSharesInstallLock(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := makeInstallArchive(t, "example", "1.0.0", "library.so", []byte("installed library"))
	file := writeInstallArchive(t, archive, "registered")
	_, err := InstallPackage(Config{Level: ConfigEnv, Location: second}, "example", file, installExpected("example", "registered", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	info, err := loadDriverFromManifest(second, "example")
	if err != nil {
		t.Fatal(err)
	}
	lockLocation, err := driverInstallLockLocation(Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second}, info)
	if err != nil {
		t.Fatal(err)
	}
	releaseLock, err := acquireDriverInstallLock(lockLocation, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- UninstallDriver(Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second}, info)
	}()
	select {
	case err := <-done:
		releaseLock()
		t.Fatalf("uninstall did not wait on the install lock for the registered path: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	releaseLock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UninstallDriver returned an error after lock release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UninstallDriver did not finish after lock release")
	}
}

func TestUninstallDriverRejectsStaleRegistrationAfterWaiting(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	oldDir := filepath.Join(root, "example_linux_amd64_v1.0.0")
	if runtime.GOOS == "darwin" {
		oldDir = filepath.Join(root, "example_macos_"+runtime.GOARCH+"_v1.0.0")
	} else {
		oldDir = filepath.Join(root, "example_"+PlatformTuple()+"_v1.0.0")
	}
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldLibrary := filepath.Join(oldDir, "old.so")
	if err := os.WriteFile(oldLibrary, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := DriverInfo{ID: "example", Name: "Old", Version: semver.MustParse("1.0.0"), Source: "dbc"}
	old.Driver.Shared.defaultPath = oldLibrary
	if err := CreateManifest(cfg, old); err != nil {
		t.Fatal(err)
	}
	stale, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	lockLocation, err := driverInstallLockLocation(cfg, stale)
	if err != nil {
		t.Fatal(err)
	}
	releaseLock, err := acquireDriverInstallLock(lockLocation, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- UninstallDriver(cfg, stale) }()
	select {
	case err := <-done:
		releaseLock()
		t.Fatalf("uninstall completed while the lock was held: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	newLibrary := filepath.Join(root, "new-generation", "new.so")
	if err := os.MkdirAll(filepath.Dir(newLibrary), 0o755); err != nil {
		releaseLock()
		t.Fatal(err)
	}
	if err := os.WriteFile(newLibrary, []byte("new"), 0o644); err != nil {
		releaseLock()
		t.Fatal(err)
	}
	updated := DriverInfo{ID: "example", Name: "New", Version: semver.MustParse("2.0.0"), Source: "dbc"}
	updated.Driver.Shared.defaultPath = newLibrary
	if err := CreateManifest(cfg, updated); err != nil {
		releaseLock()
		t.Fatal(err)
	}
	releaseLock()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "registration changed") {
			t.Fatalf("UninstallDriver error = %v, want stale-registration error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UninstallDriver did not finish after lock release")
	}
	current, err := GetDriver(cfg, "example")
	if err != nil || current.Version.String() != "2.0.0" {
		t.Fatalf("new registration was not preserved: %#v, %v", current, err)
	}
	if data, err := os.ReadFile(newLibrary); err != nil || string(data) != "new" {
		t.Fatalf("new library was not preserved: %q, %v", data, err)
	}
}

func TestInstallPackageCleansLegacyGenerationOnlyAfterSuccessfulRegistration(t *testing.T) {
	for _, test := range []struct {
		name          string
		verify        func(string, Manifest) error
		registerError error
	}{
		{name: "successful registration"},
		{name: "verification failure", verify: func(string, Manifest) error { return errors.New("verification failed") }},
		{name: "registration failure", registerError: errors.New("registration failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			legacy := createLegacyInstall(t, root, "example", "1.0.0", "")
			register := func(cfg Config, driver DriverInfo) error {
				if test.registerError != nil {
					return test.registerError
				}
				return CreateManifest(cfg, driver)
			}
			err := installVersionTwo(t, Config{Level: ConfigEnv, Location: root}, "example", test.verify, register)
			if test.registerError != nil && !errors.Is(err, test.registerError) {
				t.Fatalf("InstallPackage error = %v, want registration error", err)
			}
			if test.verify != nil && (err == nil || !strings.Contains(err.Error(), "verification failed")) {
				t.Fatalf("InstallPackage error = %v, want verification error", err)
			}
			if test.verify == nil && test.registerError == nil && err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(legacy)
			if test.verify == nil && test.registerError == nil {
				if !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("successful install retained legacy generation: %v", statErr)
				}
			} else if statErr != nil {
				t.Fatalf("failed install removed legacy generation: %v", statErr)
			}
		})
	}
}

func TestInstallPackageRetainsUnprovenLegacyCandidates(t *testing.T) {
	for _, test := range []struct {
		name        string
		setup       func(t *testing.T, root, knownCandidate string) string
		newManifest bool
	}{
		{name: "external shared file", setup: func(t *testing.T, root, candidate string) string {
			t.Helper()
			if err := os.Mkdir(candidate, 0o755); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(root, "external", "old.so")
			if err := os.MkdirAll(filepath.Dir(external), 0o755); err != nil {
				t.Fatal(err)
			}
			return createLegacyInstall(t, root, "example", "1.0.0", external)
		}},
		{name: "symlink candidate", setup: func(t *testing.T, root, candidate string) string {
			t.Helper()
			target := filepath.Join(root, "outside")
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			shared := filepath.Join(target, "old.so")
			if err := os.WriteFile(shared, []byte("old library"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, candidate); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			return createLegacyInstall(t, root, "example", "1.0.0", shared)
		}},
		{name: "unknown directory name", setup: func(t *testing.T, root, candidate string) string {
			t.Helper()
			unknown := filepath.Join(root, "example-old-generation")
			if err := os.Mkdir(unknown, 0o755); err != nil {
				t.Fatal(err)
			}
			shared := filepath.Join(unknown, "old.so")
			if err := os.WriteFile(shared, []byte("old library"), 0o644); err != nil {
				t.Fatal(err)
			}
			return createLegacyInstall(t, root, "example", "1.0.0", shared)
		}},
		{name: "receipt candidate", setup: func(t *testing.T, root, candidate string) string {
			t.Helper()
			legacy := createLegacyInstall(t, root, "example", "1.0.0", "")
			if err := os.WriteFile(filepath.Join(legacy, installReceiptName), []byte("broken"), 0o600); err != nil {
				t.Fatal(err)
			}
			return legacy
		}},
		{name: "new manifest references legacy directory", setup: func(t *testing.T, root, candidate string) string {
			t.Helper()
			return createLegacyInstall(t, root, "example", "1.0.0", "")
		}, newManifest: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			candidate := filepath.Join(root, "example_"+PlatformTuple()+"_v1.0.0")
			legacy := test.setup(t, root, candidate)
			if test.newManifest {
				oldLibrary := filepath.Join(legacy, "old-library.so")
				archiveManifest := []byte(fmt.Sprintf(`manifest_version = 1
name = "Example Driver"
version = "2.0.0"

[Driver]
shared = %q
`, oldLibrary))
				archive := makeInstallArchiveWithEntries(t, installArchiveEntry{name: "MANIFEST", data: archiveManifest})
				file := writeInstallArchive(t, archive, "references-legacy")
				expected := installExpected("example", "new-source", archive)
				expected.Version = "2.0.0"
				_, err := InstallPackage(Config{Level: ConfigEnv, Location: root}, "example", file, expected, InstallOptions{})
				_ = file.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else if err := installVersionTwo(t, Config{Level: ConfigEnv, Location: root}, "example", nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(legacy); err != nil {
				t.Fatalf("unproven legacy candidate was removed: %v", err)
			}
		})
	}
}

func TestInstallIntoFirstEnvironmentPathPreservesLaterLegacyInstall(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := createLegacyInstall(t, second, "example", "1.0.0", "")
	if err := installVersionTwo(t, Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second}, "example", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("install into first path removed later legacy generation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, "example.toml")); err != nil {
		t.Fatalf("later runtime registration was removed: %v", err)
	}
}
