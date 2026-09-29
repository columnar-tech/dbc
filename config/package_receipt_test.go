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
	_, err := InstallPackage(cfg, "driver", archive, InstallPackageOptions{Verifier: func(stage string, _ Manifest) error {
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
	if receipt.LibraryKind != packageLibraryFile || receipt.OwnedLibraryFilename != "driver.so" {
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
}

func TestInstallPackageWritesExternalReceiptWithoutOwnedLibrary(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := writeCustomPackageArchive(t, "name = \"External\"\nversion = \"1.0.0\"\n[Driver]\nshared = \"/external/lib.so\"\n")
	_, err := InstallPackage(cfg, "external", archive, InstallPackageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := GetDriver(cfg, "external")
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Join(root, packageGenerationNames(t, root, "external")[0])
	receipt, ok := readPackageInstallReceipt(root, generation)
	if !ok {
		t.Fatal("manifest-only generation has no valid receipt")
	}
	if receipt.LibraryKind != packageLibraryExternal || receipt.OwnedLibraryFilename != "" || receipt.OwnedLibrarySHA256 != "" {
		t.Fatalf("external receipt claims package bytes: %+v", receipt)
	}
	if registered.Driver.Shared.Get(PlatformTuple()) != "/external/lib.so" {
		t.Fatalf("external registration = %q", registered.Driver.Shared.Get(PlatformTuple()))
	}
	data, err := os.ReadFile(filepath.Join(generation, packageInstallReceiptFilename))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/external/lib.so") {
		t.Fatalf("receipt serialized external reference: %s", data)
	}
}

func TestInstallPackageReceiptFailurePreservesPreviousRegistration(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
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
	installInitialPackage(t, cfg)
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
	first, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, packageLibraryFile, "driver.so")
	if err != nil {
		t.Fatal(err)
	}
	info.FilePath = "/unrelated/registration/location"
	info.Driver.Shared.Set("linux_amd64", "/random/generation-a/driver.so")
	info.AdbcInfo.Features.Supported = []string{"alpha", "zeta"}
	second, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, packageLibraryFile, "driver.so")
	if err != nil || second != first {
		t.Fatalf("owned fingerprint changed with generation path/feature order: %q, %v", second, err)
	}
	info.Name = "Changed name"
	metadataChanged, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, packageLibraryFile, "driver.so")
	if err != nil || metadataChanged == first {
		t.Fatalf("metadata did not change fingerprint: %q, %v", metadataChanged, err)
	}
	info = fingerprintTestDriverInfo()
	info.AdbcInfo.Features.Supported = nil
	nilFeatures, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, packageLibraryExternal, "/external/lib.so")
	if err != nil {
		t.Fatal(err)
	}
	info.AdbcInfo.Features.Supported = []string{}
	emptyFeatures, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, packageLibraryExternal, "/external/lib.so")
	if err != nil || emptyFeatures != nilFeatures {
		t.Fatalf("nil and empty feature sets differ: %q, %q, %v", nilFeatures, emptyFeatures, err)
	}
	info.AdbcInfo.Features.Supported = []string{"alpha", "alpha"}
	duplicateFeatures, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, packageLibraryExternal, "/external/lib.so")
	if err != nil {
		t.Fatal(err)
	}
	info.AdbcInfo.Features.Supported = []string{"alpha"}
	singleFeature, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, packageLibraryExternal, "/external/lib.so")
	if err != nil || duplicateFeatures != singleFeature {
		t.Fatalf("feature deduplication behavior = %q, %v", duplicateFeatures, err)
	}
	info.AdbcInfo.Features.Supported = nil
	externalChanged, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, packageLibraryExternal, "/external/other.so")
	if err != nil || externalChanged == nilFeatures {
		t.Fatalf("external ref did not change fingerprint: %q, %v", externalChanged, err)
	}
	info = fingerprintTestDriverInfo()
	canonicalVersion, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, packageLibraryFile, "driver.so")
	if err != nil {
		t.Fatal(err)
	}
	info.Version = semver.MustParse("1.2.3+build.4")
	spelledVersion, err := runtimeRegistrationFingerprint(cfg, "driver", "linux_amd64", info, packageLibraryFile, "driver.so")
	if err != nil || spelledVersion != canonicalVersion {
		t.Fatalf("canonical semver spellings differ: %q, %q, %v", canonicalVersion, spelledVersion, err)
	}
}

func TestReceiptReaderRejectsMalformedEvidence(t *testing.T) {
	root := t.TempDir()
	generation := filepath.Join(root, ".dbc-package-driver-generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	valid := packageInstallReceipt{
		SchemaVersion:                packageInstallReceiptVersion,
		RuntimeID:                    "driver",
		DriverVersion:                "1.0.0",
		Platform:                     "linux_amd64",
		Generation:                   filepath.Base(generation),
		LibraryKind:                  packageLibraryExternal,
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
	invalid.OwnedLibraryFilename = "driver.so"
	write(invalid)
	if _, ok := readPackageInstallReceipt(root, generation); ok {
		t.Fatal("external receipt with owned filename was accepted")
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

func TestReceiptReaderRejectsUnknownSchemaAndFields(t *testing.T) {
	root := t.TempDir()
	generation := filepath.Join(root, ".dbc-package-driver-generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	valid := packageInstallReceipt{
		SchemaVersion:                packageInstallReceiptVersion,
		RuntimeID:                    "driver",
		DriverVersion:                "1.0.0",
		Platform:                     "linux_amd64",
		Generation:                   filepath.Base(generation),
		LibraryKind:                  packageLibraryExternal,
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
	badGeneration.Generation = ".dbc-package-driver-other"
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
	generation := filepath.Join(root, ".dbc-package-driver-generation")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	valid := packageInstallReceipt{
		SchemaVersion:                packageInstallReceiptVersion,
		RuntimeID:                    "driver",
		DriverVersion:                "1.0.0",
		Platform:                     "linux_amd64",
		Generation:                   filepath.Base(generation),
		LibraryKind:                  packageLibraryExternal,
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
	actualGeneration := filepath.Join(t.TempDir(), ".dbc-package-driver-actual")
	if err := os.Mkdir(actualGeneration, 0o700); err != nil {
		t.Fatal(err)
	}
	receipt := packageInstallReceipt{
		SchemaVersion:                packageInstallReceiptVersion,
		RuntimeID:                    "driver",
		DriverVersion:                "1.0.0",
		Platform:                     "linux_amd64",
		Generation:                   filepath.Base(actualGeneration),
		LibraryKind:                  packageLibraryExternal,
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
	receiptLinkGeneration := filepath.Join(root, ".dbc-package-driver-receipt-link")
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
	generationLink := filepath.Join(root, ".dbc-package-driver-generation-link")
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
