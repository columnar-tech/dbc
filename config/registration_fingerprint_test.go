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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func fingerprintFixture() (DriverInfo, registrationSharedIdentity) {
	info := DriverInfo{
		ID: "example", Name: "Example Driver", Publisher: "Example Publisher",
		License: "Apache-2.0", Source: "dbc",
	}
	info.Version = semver.MustParse("1.2.3+driver.7")
	info.AdbcInfo.Version = semver.MustParse("0.8.1+abi.2")
	info.AdbcInfo.Features.Supported = []string{"feature-b", "feature-a", "feature-a"}
	info.AdbcInfo.Features.Unsupported = nil
	info.Driver.Entrypoint = "AdbcDriverExampleInit"
	info.Driver.Shared.Set(PlatformTuple(), "/tmp/random-generation/driver.so")
	return info, registrationSharedIdentity{Kind: "package_file", PackageFile: "driver.so"}
}

func fingerprintForTest(t *testing.T, info DriverInfo, identity registrationSharedIdentity) string {
	t.Helper()
	fingerprint, err := runtimeRegistrationFingerprint(info, PlatformTuple(), identity)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func TestRuntimeRegistrationFingerprintCanonicalizesOnlyDeclaredFields(t *testing.T) {
	base, identity := fingerprintFixture()
	baseFingerprint := fingerprintForTest(t, base, identity)
	mutations := []struct {
		name   string
		mutate func(*DriverInfo)
	}{
		{name: "id", mutate: func(info *DriverInfo) { info.ID = "other" }},
		{name: "source", mutate: func(info *DriverInfo) { info.Source = "external" }},
		{name: "name", mutate: func(info *DriverInfo) { info.Name = "Other Driver" }},
		{name: "publisher", mutate: func(info *DriverInfo) { info.Publisher = "Other Publisher" }},
		{name: "license", mutate: func(info *DriverInfo) { info.License = "MIT" }},
		{name: "entrypoint", mutate: func(info *DriverInfo) { info.Driver.Entrypoint = "OtherInit" }},
		{name: "driver version", mutate: func(info *DriverInfo) { info.Version = semver.MustParse("1.2.4+driver.7") }},
		{name: "adbc version", mutate: func(info *DriverInfo) { info.AdbcInfo.Version = semver.MustParse("0.8.2+abi.2") }},
		{name: "supported features", mutate: func(info *DriverInfo) { info.AdbcInfo.Features.Supported = []string{"feature-a"} }},
		{name: "unsupported features", mutate: func(info *DriverInfo) { info.AdbcInfo.Features.Unsupported = []string{"feature-d"} }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := base
			mutation.mutate(&changed)
			if fingerprintForTest(t, changed, identity) == baseFingerprint {
				t.Fatal("runtime metadata change retained the same registration fingerprint")
			}
		})
	}

	otherGeneration := base
	otherGeneration.FilePath = "/other/config/root"
	otherGeneration.Driver.Shared.Set(PlatformTuple(), "/other/config/root/.dbc-package-example-random/driver.so")
	if got := fingerprintForTest(t, otherGeneration, identity); got != baseFingerprint {
		t.Fatalf("generation directory changed fingerprint: got %s, want %s", got, baseFingerprint)
	}

	spelling, err := semver.NewVersion("v1.2.3+driver.7")
	if err != nil {
		t.Fatal(err)
	}
	spellingInfo := base
	spellingInfo.Version = spelling
	if spelling.String() != base.Version.String() {
		t.Fatalf("test semver spellings are not equivalent under String: %q != %q", spelling.String(), base.Version.String())
	}
	spellingInfo.AdbcInfo.Features.Supported = []string{"feature-a", "feature-b"}
	spellingInfo.AdbcInfo.Features.Unsupported = []string{}
	if got := fingerprintForTest(t, spellingInfo, identity); got != baseFingerprint {
		t.Fatalf("semver spelling/features order and duplicates changed fingerprint: got %s, want %s", got, baseFingerprint)
	}

	buildMetadata := base
	buildMetadata.Version = semver.MustParse("1.2.3+driver.8")
	if got := fingerprintForTest(t, buildMetadata, identity); got == baseFingerprint {
		t.Fatal("semver build metadata was omitted from the registration fingerprint")
	}
	missingADBC := base
	missingADBC.AdbcInfo.Version = nil
	if got := fingerprintForTest(t, missingADBC, identity); got == baseFingerprint {
		t.Fatal("nil ADBC version did not differ from an explicit version")
	}
}

func TestRuntimeRegistrationFingerprintTagsSharedIdentity(t *testing.T) {
	info, owned := fingerprintFixture()
	ownedFingerprint := fingerprintForTest(t, info, owned)
	external := registrationSharedIdentity{Kind: "external", ExternalReference: "/opt/example/driver.so"}
	externalFingerprint := fingerprintForTest(t, info, external)
	if externalFingerprint == ownedFingerprint {
		t.Fatal("owned package file and external shared reference had the same fingerprint")
	}
	otherExternal := registrationSharedIdentity{Kind: "external", ExternalReference: "/opt/example/other.so"}
	if fingerprintForTest(t, info, otherExternal) == externalFingerprint {
		t.Fatal("different external shared references had the same fingerprint")
	}
	caseChanged := registrationSharedIdentity{Kind: "external", ExternalReference: "/opt/example/Driver.so"}
	if fingerprintForTest(t, info, caseChanged) == externalFingerprint {
		t.Fatal("external reference case was unexpectedly normalized")
	}
}

func TestPackageValidationFingerprintMatchesInstalledReceipt(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := makeInstallArchive(t, "example", "1.2.3", "driver.so", []byte("library"))
	expected := installExpected("example", "candidate-source", archive)
	expected.Version = "1.2.3"
	validationFile := writeInstallArchive(t, archive, "fingerprint-validation")
	defer validationFile.Close()
	validation, err := PreparePackage(cfg, "example", validationFile, expected, InstallOptions{})
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
	if validation.RegistrationFingerprintAlgorithm != registrationFingerprintAlgorithm || validation.RegistrationFingerprintVersion != registrationFingerprintVersion {
		t.Fatalf("validation fingerprint algorithm/version = %q/%d", validation.RegistrationFingerprintAlgorithm, validation.RegistrationFingerprintVersion)
	}
	installedFile := writeInstallArchive(t, archive, "fingerprint-install")
	installed, err := InstallPackage(cfg, "example", installedFile, expected, InstallOptions{})
	_ = installedFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	current, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	if current.Driver.Shared.Get(PlatformTuple()) == validation.Registration.Driver.Shared.Get(PlatformTuple()) {
		t.Fatal("validation and install unexpectedly used the same generation path")
	}
	receipt, managed, present, valid, err := InspectDriverInstallReceipt(cfg, current)
	if err != nil || !managed || !present || !valid {
		t.Fatalf("installed receipt inspection managed=%t present=%t valid=%t error=%v", managed, present, valid, err)
	}
	if receipt.RegistrationFingerprint != validation.RegistrationFingerprint {
		t.Fatalf("validation fingerprint %q differs from installed receipt %q", validation.RegistrationFingerprint, receipt.RegistrationFingerprint)
	}
	if !PackageValidationMatchesRuntimeRegistration(current, validation, PlatformTuple()) {
		t.Fatal("validated package fingerprint did not match the installed runtime registration")
	}
	if !InstallReceiptMatchesRuntimeRegistration(receipt, current, PlatformTuple()) {
		t.Fatal("receipt fingerprint did not match the installed runtime registration")
	}
	if !VerifyInstallReceiptLibraryIntegrity(installed.Driver.Shared.Get(PlatformTuple()), receipt) {
		t.Fatal("installed library did not match its receipt")
	}

	tampered := current
	tampered.Name = "Tampered Runtime Name"
	if err := CreateManifest(cfg, tampered); err != nil {
		t.Fatal(err)
	}
	tamperedCurrent, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	if InstallReceiptMatchesRuntimeRegistration(receipt, tamperedCurrent, PlatformTuple()) {
		t.Fatal("receipt fingerprint accepted tampered runtime metadata")
	}
	if PackageValidationMatchesRuntimeRegistration(tamperedCurrent, validation, PlatformTuple()) {
		t.Fatal("candidate fingerprint accepted tampered runtime metadata")
	}
}

func TestFingerprintReceiptCompatibilityAndCleanup(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*InstallReceipt)
	}{
		{name: "legacy missing fields", mutate: func(receipt *InstallReceipt) {
			receipt.RegistrationFingerprintAlgorithm = ""
			receipt.RegistrationFingerprintVersion = 0
			receipt.RegistrationFingerprint = ""
		}},
		{name: "partial fields", mutate: func(receipt *InstallReceipt) {
			receipt.RegistrationFingerprintAlgorithm = registrationFingerprintAlgorithm
			receipt.RegistrationFingerprintVersion = 0
		}},
		{name: "unknown schema", mutate: func(receipt *InstallReceipt) {
			receipt.RegistrationFingerprintVersion++
		}},
		{name: "malformed digest", mutate: func(receipt *InstallReceipt) {
			receipt.RegistrationFingerprint = "sha256:bad"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := Config{Level: ConfigEnv, Location: root}
			archive := makeInstallArchive(t, "example", "1.0.0", "driver.so", []byte("old library"))
			firstFile := writeInstallArchive(t, archive, "old-fingerprint")
			installed, err := InstallPackage(cfg, "example", firstFile, installExpected("example", "source", archive), InstallOptions{})
			_ = firstFile.Close()
			if err != nil {
				t.Fatal(err)
			}
			current, err := GetDriver(cfg, "example")
			if err != nil {
				t.Fatal(err)
			}
			receiptPath := filepath.Join(filepath.Dir(installed.Driver.Shared.Get(PlatformTuple())), installReceiptName)
			receiptData, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}
			var receipt InstallReceipt
			if err := json.Unmarshal(receiptData, &receipt); err != nil {
				t.Fatal(err)
			}
			test.mutate(&receipt)
			mutatedData, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(receiptPath, mutatedData, 0o600); err != nil {
				t.Fatal(err)
			}
			inspected, managed, present, valid, err := InspectDriverInstallReceipt(cfg, current)
			if err != nil || !managed || !present || !valid {
				t.Fatalf("fingerprint metadata changed receipt inspection: managed=%t present=%t valid=%t error=%v", managed, present, valid, err)
			}
			if InstallReceiptMatchesRuntimeRegistration(inspected, current, PlatformTuple()) {
				t.Fatal("legacy/unknown/malformed fingerprint was accepted as reuse proof")
			}

			nextArchive := makeInstallArchive(t, "example", "2.0.0", "new-driver.so", []byte("new library"))
			nextFile := writeInstallArchive(t, nextArchive, "new-fingerprint")
			nextExpected := installExpected("example", "next-source", nextArchive)
			nextExpected.Version = "2.0.0"
			_, err = InstallPackage(cfg, "example", nextFile, nextExpected, InstallOptions{})
			_ = nextFile.Close()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Dir(installed.Driver.Shared.Get(PlatformTuple()))); !os.IsNotExist(err) {
				t.Fatalf("cleanup did not preserve receipt compatibility; old generation stat error = %v", err)
			}
		})
	}
}

func TestSameRuntimeDriverRegistrationUsesSemanticVersionsAndFeatureSets(t *testing.T) {
	first, identity := fingerprintFixture()
	second := first
	second.FilePath = "/another/registration/root"
	second.Version = semver.MustParse("v1.2.3+driver.7")
	second.AdbcInfo.Version = semver.MustParse("v0.8.1+abi.2")
	second.AdbcInfo.Features.Supported = []string{"feature-a", "feature-b", "feature-a"}
	second.AdbcInfo.Features.Unsupported = nil
	if !SameRuntimeDriverRegistration(first, second, PlatformTuple()) {
		t.Fatal("canonical semver equality and feature set equality were not applied")
	}
	buildMetadataChange := second
	buildMetadataChange.Version = semver.MustParse("1.2.3+different-build")
	if SameRuntimeDriverRegistration(first, buildMetadataChange, PlatformTuple()) {
		t.Fatal("driver version build metadata difference was ignored")
	}
	buildMetadataChange = second
	buildMetadataChange.AdbcInfo.Version = semver.MustParse("0.8.1+different-build")
	if SameRuntimeDriverRegistration(first, buildMetadataChange, PlatformTuple()) {
		t.Fatal("ADBC version build metadata difference was ignored")
	}
	second.Driver.Shared = driverMap{}
	second.Driver.Shared.Set(PlatformTuple(), "/opt/another/driver.so")
	if SameRuntimeDriverRegistration(first, second, PlatformTuple()) {
		t.Fatal("external shared reference change was ignored")
	}
	if _, err := runtimeRegistrationFingerprint(first, PlatformTuple(), identity); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationFingerprintDigestIsCanonicalSHA256(t *testing.T) {
	info, identity := fingerprintFixture()
	fingerprint := fingerprintForTest(t, info, identity)
	if !strings.HasPrefix(fingerprint, "sha256:") || !validRegistrationFingerprint("sha256", registrationFingerprintVersion, fingerprint) {
		t.Fatalf("registration fingerprint is not canonical SHA-256: %q", fingerprint)
	}
	if validRegistrationFingerprint("future", registrationFingerprintVersion, fingerprint) ||
		validRegistrationFingerprint("sha256", registrationFingerprintVersion+1, fingerprint) ||
		validRegistrationFingerprint("sha256", registrationFingerprintVersion, "sha256:malformed") {
		t.Fatal("unknown or malformed fingerprint metadata was accepted")
	}
}
