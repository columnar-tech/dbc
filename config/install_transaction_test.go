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
	"strings"
	"sync"
	"testing"
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
		installArchiveEntry{name: "MANIFEST", data: manifest},
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
