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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Masterminds/semver/v3"
)

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
		{name: "explicit zero archive size mismatch", mutate: func(expected *ExpectedPackageMetadata) {
			expected.ArchiveSize = 0
			expected.ArchiveSizePresent = true
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

func TestInstallPackageHashOnlyMetadataRetainsMeasuredSizeAndMatchesReceipt(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("hash-only archive"))
	expected := installExpected("example", "hash-only-source", archive)
	expected.ArchiveSize = 0
	file := writeInstallArchive(t, archive, "hash-only")
	installed, err := InstallPackage(cfg, "example", file, expected, InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}

	receiptData, err := os.ReadFile(filepath.Join(filepath.Dir(installed.Driver.Shared.Get(PlatformTuple())), installReceiptName))
	if err != nil {
		t.Fatal(err)
	}
	var receipt InstallReceipt
	if err := json.Unmarshal(receiptData, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ArchiveSize != int64(len(archive)) || receipt.ArchiveSize <= 0 {
		t.Fatalf("receipt archive size = %d, want measured positive size %d", receipt.ArchiveSize, len(archive))
	}
	if !InstallReceiptMatchesExpectedPackage(receipt, expected) {
		t.Fatal("hash-only expected metadata did not match its installation receipt")
	}

	wrongZero := expected
	wrongZero.ArchiveSizePresent = true
	if InstallReceiptMatchesExpectedPackage(receipt, wrongZero) {
		t.Fatal("receipt matched an explicitly expected zero archive size")
	}
	wrongSize := expected
	wrongSize.ArchiveSize = receipt.ArchiveSize + 1
	if InstallReceiptMatchesExpectedPackage(receipt, wrongSize) {
		t.Fatal("receipt matched a mismatched expected archive size")
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

func TestPreparePackageReturnsAndComparesRuntimeRegistration(t *testing.T) {
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
	cfg := Config{Level: ConfigEnv, Location: root}
	validation, err := PreparePackage(cfg, "example", file, installExpected("example", "source", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if validation.Prepared == nil {
		t.Fatal("package preparation returned no prepared payload")
	}
	t.Cleanup(func() {
		if err := validation.Prepared.Close(); err != nil {
			t.Errorf("close prepared package: %v", err)
		}
	})
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
