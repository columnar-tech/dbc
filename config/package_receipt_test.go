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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestInstallPackageWritesOwnedLibraryReceipt(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := testPackageArchive(t, "verified-bytes")
	_, err := InstallPackage(context.Background(), cfg, "driver", archive, InstallPackageOptions{Verifier: func(stage string, _ Manifest) error {
		return os.WriteFile(filepath.Join(stage, "driver.so"), []byte("post verifier bytes"), 0o600)
	}})
	if err != nil {
		t.Fatal(err)
	}
	assertArchiveClosed(t, archive)
	registered, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Dir(registered.Driver.Shared.Get(PlatformTuple()))
	receipt, ok := readPackageInstallReceipt(root, generation)
	if !ok {
		t.Fatal("installed generation has no valid receipt")
	}
	if receipt.SchemaVersion != packageInstallReceiptVersion || receipt.RuntimeID != "driver" || receipt.DriverVersion != "1.0.0" || receipt.Platform != PlatformTuple() || receipt.Generation != filepath.Base(generation) {
		t.Fatalf("receipt identity = %+v", receipt)
	}
	wantScope, err := packageRegistrationScopeForConfig(cfg)
	if err != nil || receipt.RegistrationScope != wantScope {
		t.Fatalf("receipt registration scope = %q, want %q: %v", receipt.RegistrationScope, wantScope, err)
	}
	if receipt.OwnedLibraryFilename != "driver.so" {
		t.Fatalf("receipt ownership = %+v", receipt)
	}
	expected := sha256.Sum256([]byte("post verifier bytes"))
	if receipt.OwnedLibrarySHA256 != hex.EncodeToString(expected[:]) {
		t.Fatalf("owned hash = %q, want %x", receipt.OwnedLibrarySHA256, expected)
	}
	data, err := os.ReadFile(filepath.Join(generation, packageInstallReceiptFilename))
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"url", "source_identity", "archive_hash", "archive_size", "source_type", "registry", "path"} {
		if _, exists := keys[key]; exists {
			t.Fatalf("source-specific key %q in receipt", key)
		}
	}
	for _, key := range []string{"owned_library_filename", "owned_library_sha256"} {
		if _, exists := keys[key]; !exists {
			t.Fatalf("required ownership key %q is missing from receipt", key)
		}
	}
	if _, exists := keys["library_kind"]; exists {
		t.Fatal("owned receipt retains the removed library-kind distinction")
	}
}

func TestPackageRegistrationScopesAreValidatedAndDistinct(t *testing.T) {
	for _, scope := range []packageRegistrationScope{packageRegistrationFile, packageRegistrationRegistryUser, packageRegistrationRegistrySystem} {
		if !validPackageRegistrationScope(scope) {
			t.Errorf("known registration scope %q was rejected", scope)
		}
	}
	if validPackageRegistrationScope("other") {
		t.Fatal("unknown registration scope was accepted")
	}
}

func TestWritePackageInstallReceiptEnforcesFinalSizeLimit(t *testing.T) {
	root := t.TempDir()
	generation := testPackageGenerationPath(t, root, "driver", "generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	receipt := packageInstallReceipt{
		SchemaVersion:                packageInstallReceiptVersion,
		RegistrationScope:            packageRegistrationFile,
		RuntimeID:                    "driver",
		DriverVersion:                "1.0.0",
		Platform:                     "linux_amd64",
		Generation:                   filepath.Base(generation),
		OwnedLibraryFilename:         "driver.so",
		OwnedLibrarySHA256:           strings.Repeat("a", sha256.Size*2),
		RegistrationFingerprintAlgo:  registrationFingerprintName,
		RegistrationFingerprintVer:   registrationFingerprintVer,
		RegistrationFingerprintValue: strings.Repeat("a", sha256.Size*2),
	}
	if err := writePackageInstallReceipt(generation, receipt); err != nil {
		t.Fatalf("write receipt below size limit: %v", err)
	}
	if _, ok := readPackageInstallReceipt(root, generation); !ok {
		t.Fatal("receipt written below size limit was not readable")
	}
	if err := os.Remove(filepath.Join(generation, packageInstallReceiptFilename)); err != nil {
		t.Fatal(err)
	}

	receipt.DriverVersion = strings.Repeat("x", packageInstallReceiptMaxSize)
	err := writePackageInstallReceipt(generation, receipt)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("oversized receipt write error = %v, want clear size-limit error", err)
	}
	if _, err := os.Lstat(filepath.Join(generation, packageInstallReceiptFilename)); !os.IsNotExist(err) {
		t.Fatalf("oversized receipt created a file before rejection: %v", err)
	}
}

func TestInstallPackageReceiptFailurePreservesPreviousRegistration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialTransactionPackage(t, cfg)
	previous, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	previousPath := previous.Driver.Shared.Get(PlatformTuple())
	archive := testPackageArchive(t, "new")
	writeErr := errors.New("injected receipt failure")
	operations := testPackageInstallOperations()
	operations.writeReceipt = func(string, packageInstallReceipt) error { return writeErr }
	_, err = installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations)
	if !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "write package install receipt") {
		t.Fatalf("InstallPackage error = %v", err)
	}
	assertArchiveClosed(t, archive)
	assertPreviousPackage(t, cfg, previousPath)
	assertOnlyPackageGeneration(t, root, filepath.Base(filepath.Dir(previousPath)))
}

func TestInstallPackageRollbackFailurePreservesReceipt(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialTransactionPackage(t, cfg)
	previous, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	previousPath := previous.Driver.Shared.Get(PlatformTuple())
	archive := testPackageArchive(t, "candidate")
	operations := testPackageInstallOperations()
	var candidate string
	operations.register = func(_ Config, _ string, info DriverInfo) error {
		candidate = filepath.Dir(info.Driver.Shared.Get(PlatformTuple()))
		return errRegistrationRollbackFailed
	}
	_, err = installPackageWithOperations(cfg, "driver", archive, InstallPackageOptions{}, operations)
	if !errors.Is(err, errRegistrationRollbackFailed) {
		t.Fatalf("InstallPackage error = %v", err)
	}
	assertPreviousPackage(t, cfg, previousPath)
	if _, err := os.Stat(filepath.Join(candidate, packageInstallReceiptFilename)); err != nil {
		t.Fatalf("rollback candidate receipt was not preserved: %v", err)
	}
	if _, ok := readPackageInstallReceipt(root, candidate); !ok {
		t.Fatal("rollback candidate receipt is invalid")
	}
}

func TestRuntimeRegistrationFingerprintCanonicalization(t *testing.T) {
	cfg := Config{Level: ConfigEnv}
	info := fingerprintTestDriverInfo()
	info.AdbcInfo.Features.Supported = []string{"zeta", "alpha", "zeta"}
	first, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, "driver.so")
	if err != nil {
		t.Fatal(err)
	}
	info.FilePath = "/unrelated/registration/location"
	info.Driver.Shared.Set("linux_amd64", "/random/generation-a/driver.so")
	info.AdbcInfo.Features.Supported = []string{"alpha", "zeta"}
	second, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, "driver.so")
	if err != nil || second != first {
		t.Fatalf("owned fingerprint changed with generation path/feature order: %q, %v", second, err)
	}
	info.Name = "Changed name"
	metadataChanged, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, "driver.so")
	if err != nil || metadataChanged == first {
		t.Fatalf("metadata did not change fingerprint: %q, %v", metadataChanged, err)
	}
	info = fingerprintTestDriverInfo()
	info.AdbcInfo.Features.Supported = nil
	nilFeatures, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, "driver.so")
	if err != nil {
		t.Fatal(err)
	}
	info.AdbcInfo.Features.Supported = []string{}
	emptyFeatures, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, "driver.so")
	if err != nil || emptyFeatures != nilFeatures {
		t.Fatalf("nil and empty feature sets differ: %q, %q, %v", nilFeatures, emptyFeatures, err)
	}
	info.AdbcInfo.Features.Supported = []string{"alpha", "alpha"}
	duplicateFeatures, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, "driver.so")
	if err != nil {
		t.Fatal(err)
	}
	info.AdbcInfo.Features.Supported = []string{"alpha"}
	singleFeature, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, "driver.so")
	if err != nil || duplicateFeatures != singleFeature {
		t.Fatalf("feature deduplication behavior = %q, %v", duplicateFeatures, err)
	}
	info.AdbcInfo.Features.Supported = nil
	otherOwnedFile, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, "other.so")
	if err != nil || otherOwnedFile == nilFeatures {
		t.Fatalf("owned filename did not change fingerprint: %q, %v", otherOwnedFile, err)
	}
	info = fingerprintTestDriverInfo()
	canonicalVersion, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, "driver.so")
	if err != nil {
		t.Fatal(err)
	}
	info.Version = semver.MustParse("1.2.3+build.4")
	spelledVersion, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, "driver.so")
	if err != nil || spelledVersion != canonicalVersion {
		t.Fatalf("canonical semver spellings differ: %q, %q, %v", canonicalVersion, spelledVersion, err)
	}
}

func TestReceiptReaderRejectsMalformedEvidence(t *testing.T) {
	root := t.TempDir()
	generation := testPackageGenerationPath(t, root, "driver", "generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	valid := packageInstallReceipt{
		SchemaVersion:                packageInstallReceiptVersion,
		RegistrationScope:            packageRegistrationFile,
		RuntimeID:                    "driver",
		DriverVersion:                "1.0.0",
		Platform:                     "linux_amd64",
		Generation:                   filepath.Base(generation),
		OwnedLibraryFilename:         "driver.so",
		OwnedLibrarySHA256:           strings.Repeat("b", sha256.Size*2),
		RegistrationFingerprintAlgo:  registrationFingerprintName,
		RegistrationFingerprintVer:   registrationFingerprintVer,
		RegistrationFingerprintValue: strings.Repeat("a", sha256.Size*2),
	}
	write := func(receipt packageInstallReceipt) {
		t.Helper()
		data, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(generation, packageInstallReceiptFilename), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(valid)
	if _, ok := readPackageInstallReceipt(root, generation); !ok {
		t.Fatal("valid receipt was rejected")
	}
	invalid := valid
	invalid.OwnedLibraryFilename = "../driver.so"
	write(invalid)
	if _, ok := readPackageInstallReceipt(root, generation); ok {
		t.Fatal("receipt with an unsafe owned filename was accepted")
	}
	invalid = valid
	invalid.RegistrationScope = "unknown"
	write(invalid)
	if _, ok := readPackageInstallReceipt(root, generation); ok {
		t.Fatal("receipt with unknown registration scope was accepted")
	}
	invalid = valid
	invalid.Generation = filepath.Base(testPackageGenerationPath(t, root, "driver-other", "generation"))
	write(invalid)
	if _, ok := readPackageInstallReceipt(root, generation); ok {
		t.Fatal("receipt generation with mismatched runtime ID was accepted")
	}
	mismatchedGeneration := testPackageGenerationPath(t, root, "driver-other", "generation")
	if err := os.Mkdir(mismatchedGeneration, 0o700); err != nil {
		t.Fatal(err)
	}
	invalid = valid
	invalid.Generation = filepath.Base(mismatchedGeneration)
	data, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mismatchedGeneration, packageInstallReceiptFilename), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPackageInstallReceipt(root, mismatchedGeneration); ok {
		t.Fatal("receipt runtime ID did not match parsed generation ID")
	}
	noncanonicalGeneration := filepath.Join(root, ".dbc-package-g-06-driver-generation")
	if err := os.Mkdir(noncanonicalGeneration, 0o700); err != nil {
		t.Fatal(err)
	}
	invalid = valid
	invalid.Generation = filepath.Base(noncanonicalGeneration)
	data, err = json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noncanonicalGeneration, packageInstallReceiptFilename), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPackageInstallReceipt(root, noncanonicalGeneration); ok {
		t.Fatal("receipt with noncanonical generation length was accepted")
	}
	if _, ok := readPackageInstallReceipt(root, filepath.Join(root, "nested", filepath.Base(generation))); ok {
		t.Fatal("receipt outside direct primary-root child was accepted")
	}
	if err := os.Remove(filepath.Join(generation, packageInstallReceiptFilename)); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPackageInstallReceipt(root, generation); ok {
		t.Fatal("missing receipt was accepted")
	}
}

func TestPackageGenerationNameParsingAndTransactionEvidence(t *testing.T) {
	for _, runtimeID := range []string{"driver", "driver-other", "stage", "réader"} {
		prefix, err := packageGenerationPrefix(runtimeID)
		if err != nil {
			t.Fatal(err)
		}
		name := prefix + "random"
		parsed, ok := parsePackageGenerationName(name)
		if !ok || parsed != runtimeID {
			t.Fatalf("parse generation %q = %q, %t", name, parsed, ok)
		}
	}
	for _, name := range []string{
		".dbc-package-g-06-driver-random",
		".dbc-package-g-6-driver",
		".dbc-package-g-6-drive-random",
		".dbc-package-g-6-réader-random",
		".dbc-package-stage-random",
		".dbc-package-driver-random",
	} {
		if runtimeID, ok := parsePackageGenerationName(name); ok {
			t.Errorf("malformed generation %q parsed as %q", name, runtimeID)
		}
	}

	root := t.TempDir()
	driverGeneration := testPackageGenerationPath(t, root, "driver", "evidence")
	driverOtherGeneration := testPackageGenerationPath(t, root, "driver-other", "evidence")
	for _, generation := range []string{driverGeneration, driverOtherGeneration} {
		if err := os.Mkdir(generation, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTransactionEvidence(entries, "driver") || !hasTransactionEvidence(entries, "driver-other") {
		t.Fatal("transaction evidence was not matched to its runtime ID")
	}
	otherRoot := t.TempDir()
	if err := os.Mkdir(testPackageGenerationPath(t, otherRoot, "driver-other", "evidence"), 0o700); err != nil {
		t.Fatal(err)
	}
	otherEntries, err := os.ReadDir(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	if hasTransactionEvidence(otherEntries, "driver") {
		t.Fatal("driver-other transaction evidence matched driver")
	}
	driverRoot := t.TempDir()
	if err := os.Mkdir(testPackageGenerationPath(t, driverRoot, "driver", "evidence"), 0o700); err != nil {
		t.Fatal(err)
	}
	driverEntries, err := os.ReadDir(driverRoot)
	if err != nil {
		t.Fatal(err)
	}
	if hasTransactionEvidence(driverEntries, "driver-other") {
		t.Fatal("driver transaction evidence matched driver-other")
	}
}

func TestReceiptReaderRejectsUnknownSchemaAndFields(t *testing.T) {
	root := t.TempDir()
	generation := testPackageGenerationPath(t, root, "driver", "generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	valid := packageInstallReceipt{
		SchemaVersion:                packageInstallReceiptVersion,
		RegistrationScope:            packageRegistrationFile,
		RuntimeID:                    "driver",
		DriverVersion:                "1.0.0",
		Platform:                     "linux_amd64",
		Generation:                   filepath.Base(generation),
		OwnedLibraryFilename:         "driver.so",
		OwnedLibrarySHA256:           strings.Repeat("b", sha256.Size*2),
		RegistrationFingerprintAlgo:  registrationFingerprintName,
		RegistrationFingerprintVer:   registrationFingerprintVer,
		RegistrationFingerprintValue: strings.Repeat("b", sha256.Size*2),
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(generation, packageInstallReceiptFilename)
	write := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(generation, packageInstallReceiptFilename), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	unknownSchema := valid
	unknownSchema.SchemaVersion++
	data, err := json.Marshal(unknownSchema)
	if err != nil {
		t.Fatal(err)
	}
	write(data)
	if _, ok := readPackageInstallReceipt(root, generation); ok {
		t.Fatal("unknown receipt schema was accepted")
	}
	write([]byte(strings.TrimSuffix(string(encoded), "}") + `,"unrecognized":true}`))
	if _, ok := readPackageInstallReceipt(root, generation); ok {
		t.Fatal("receipt with unknown field was accepted")
	}
	write(append(append([]byte(nil), encoded...), []byte(" trailing data")...))
	if _, ok := readPackageInstallReceipt(root, generation); ok {
		t.Fatal("receipt with trailing data was accepted")
	}
	badGeneration := valid
	badGeneration.Generation = filepath.Base(testPackageGenerationPath(t, root, "driver", "other"))
	data, err = json.Marshal(badGeneration)
	if err != nil {
		t.Fatal(err)
	}
	write(data)
	if _, ok := readPackageInstallReceipt(root, generation); ok {
		t.Fatal("receipt with mismatched generation was accepted")
	}
}

func TestReceiptReaderRejectsOversizedTrailingData(t *testing.T) {
	root := t.TempDir()
	generation := testPackageGenerationPath(t, root, "driver", "generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	valid := packageInstallReceipt{
		SchemaVersion:                packageInstallReceiptVersion,
		RegistrationScope:            packageRegistrationFile,
		RuntimeID:                    "driver",
		DriverVersion:                "1.0.0",
		Platform:                     "linux_amd64",
		Generation:                   filepath.Base(generation),
		OwnedLibraryFilename:         "driver.so",
		OwnedLibrarySHA256:           strings.Repeat("c", sha256.Size*2),
		RegistrationFingerprintAlgo:  registrationFingerprintName,
		RegistrationFingerprintVer:   registrationFingerprintVer,
		RegistrationFingerprintValue: strings.Repeat("c", sha256.Size*2),
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte(nil), encoded...)
	data = append(data, bytes.Repeat([]byte(" "), packageInstallReceiptMaxSize-len(data))...)
	data = append(data, '!')
	path := filepath.Join(generation, packageInstallReceiptFilename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPackageInstallReceipt(root, generation); ok {
		t.Fatal("receipt with data beyond the size limit was accepted")
	}
}

func TestReceiptReaderRejectsSymlinkedEvidence(t *testing.T) {
	root := t.TempDir()
	actualGeneration := testPackageGenerationPath(t, t.TempDir(), "driver", "actual")
	if err := os.Mkdir(actualGeneration, 0o700); err != nil {
		t.Fatal(err)
	}
	receipt := packageInstallReceipt{
		SchemaVersion:                packageInstallReceiptVersion,
		RegistrationScope:            packageRegistrationFile,
		RuntimeID:                    "driver",
		DriverVersion:                "1.0.0",
		Platform:                     "linux_amd64",
		Generation:                   filepath.Base(actualGeneration),
		OwnedLibraryFilename:         "driver.so",
		OwnedLibrarySHA256:           strings.Repeat("d", sha256.Size*2),
		RegistrationFingerprintAlgo:  registrationFingerprintName,
		RegistrationFingerprintVer:   registrationFingerprintVer,
		RegistrationFingerprintValue: strings.Repeat("d", sha256.Size*2),
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actualGeneration, packageInstallReceiptFilename), data, 0o600); err != nil {
		t.Fatal(err)
	}
	receiptLinkGeneration := testPackageGenerationPath(t, root, "driver", "receipt-link")
	if err := os.Mkdir(receiptLinkGeneration, 0o700); err != nil {
		t.Fatal(err)
	}
	receipt.Generation = filepath.Base(receiptLinkGeneration)
	data, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actualGeneration, packageInstallReceiptFilename), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(actualGeneration, packageInstallReceiptFilename), filepath.Join(receiptLinkGeneration, packageInstallReceiptFilename)); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if _, ok := readPackageInstallReceipt(root, receiptLinkGeneration); ok {
		t.Fatal("symlinked receipt was accepted")
	}
	generationLink := testPackageGenerationPath(t, root, "driver", "generation-link")
	if err := os.Symlink(actualGeneration, generationLink); err != nil {
		t.Skipf("directory symlink creation is unavailable: %v", err)
	}
	receipt.Generation = filepath.Base(generationLink)
	data, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actualGeneration, packageInstallReceiptFilename), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPackageInstallReceipt(root, generationLink); ok {
		t.Fatal("symlinked generation directory was accepted")
	}
}

func fingerprintTestDriverInfo() DriverInfo {
	info := DriverInfo{
		ID:        "driver",
		Name:      "Driver",
		Publisher: "Publisher",
		License:   "Apache-2.0",
		Source:    "dbc",
		Version:   semver.MustParse("v1.2.3+build.4"),
		Driver: struct {
			Entrypoint string
			Shared     driverMap
		}{Entrypoint: "connect", Shared: driverMap{}},
	}
	info.AdbcInfo.Version = semver.MustParse("1.0.0")
	return info
}
