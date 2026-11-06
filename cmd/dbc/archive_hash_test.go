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

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/go-faster/yaml"
)

func testArchiveSHA256(suite *SubcommandTestSuite, name string) string {
	data, err := os.ReadFile(filepath.Join("testdata", name))
	suite.Require().NoError(err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hashedRegistry returns a registry with test-driver-1 v1.0.0 publishing hash.
func hashedRegistry(hash string) func() ([]dbc.Driver, error) {
	return func() ([]dbc.Driver, error) {
		index := fmt.Sprintf(`drivers:
  - name: Test Driver 1
    description: This is a test driver
    license: MIT
    path: test-driver-1
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %s
            url: test-driver-1/1.0.0/test-driver-1.tar.gz
            hash: sha256:%s
`, config.PlatformTuple(), hash)
		var decoded struct {
			Drivers []dbc.Driver `yaml:"drivers"`
		}
		if err := yaml.Unmarshal([]byte(index), &decoded); err != nil {
			return nil, err
		}
		for i := range decoded.Drivers {
			decoded.Drivers[i].Registry = &testRegistry
		}
		return decoded.Drivers, nil
	}
}

func hashedBaseModel(hash string) baseModel {
	return baseModel{getDriverRegistry: hashedRegistry(hash), downloadPkg: downloadTestPkg}
}

func (suite *SubcommandTestSuite) TestInstallRegistryHashMatch() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.tar.gz")))
	suite.runCmd(m)
	suite.driverIsInstalled("test-driver-1", true)
}

func (suite *SubcommandTestSuite) TestInstallRegistryHashMismatch() {
	zeros := "0000000000000000000000000000000000000000000000000000000000000000"
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(hashedBaseModel(zeros))
	out := suite.runCmdErr(m)
	suite.Contains(out, "archive SHA-256 mismatch")
	suite.Contains(out, zeros)
	suite.Contains(out, testArchiveSHA256(suite, "test-driver-1.tar.gz"))
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestInstallRegistryHashMismatchJSON() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Json: true}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.1.tar.gz")))
	out := suite.runCmdErr(m)
	suite.Contains(out, "archive SHA-256 mismatch")
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestInstallRegistryHashMismatchWithNoVerify() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, NoVerify: true}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.1.tar.gz")))
	suite.Contains(suite.runCmdErr(m), "archive SHA-256 mismatch")
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestInstallAlreadyInstalledSkipsHashCheck() {
	suite.runCmd(InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, NoVerify: true}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.tar.gz"))))
	old := suite.getInstalledDriver("test-driver-1")

	// The same version is already installed: nothing is downloaded, so even a
	// registry hash that cannot match does not matter.
	downloads := 0
	base := hashedBaseModel("0000000000000000000000000000000000000000000000000000000000000000")
	base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
		downloads++
		return downloadTestPkg(pkg)
	}
	suite.runCmd(InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.GetModelCustom(base))
	suite.Zero(downloads)

	current := suite.getInstalledDriver("test-driver-1")
	suite.Equal(old.Driver.Shared.Get(config.PlatformTuple()), current.Driver.Shared.Get(config.PlatformTuple()))
}

func (suite *SubcommandTestSuite) TestSyncRegistryHashMismatch() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))

	zeros := "0000000000000000000000000000000000000000000000000000000000000000"
	m := SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(hashedBaseModel(zeros))
	out := suite.runCmdErr(m)
	suite.Contains(out, "archive SHA-256 mismatch")
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestSyncRegistryHashMatch() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))

	m := SyncCmd{Path: listPath, Level: suite.configLevel}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.tar.gz")))
	suite.runCmd(m)
	suite.driverIsInstalled("test-driver-1", true)
}

// receiptEvidence is the part of the persisted install receipt these tests
// check. SignatureVerified is a pointer so a missing field is detectable.
type receiptEvidence struct {
	ArchiveSHA256     string `json:"archive_sha256"`
	SignatureVerified *bool  `json:"signature_verified"`
}

// receiptPath returns the receipt file next to the registered shared library
// of the installed driver.
func (suite *SubcommandTestSuite) receiptPath(driver string) string {
	shared := suite.getInstalledDriver(driver).Driver.Shared.Get(config.PlatformTuple())
	return filepath.Join(filepath.Dir(shared), "dbc-install-receipt.json")
}

func (suite *SubcommandTestSuite) readReceipt(driver string) receiptEvidence {
	data, err := os.ReadFile(suite.receiptPath(driver))
	suite.Require().NoError(err)
	var r receiptEvidence
	suite.Require().NoError(json.Unmarshal(data, &r))
	suite.Require().NotNil(r.SignatureVerified, "receipt must record signature_verified")
	return r
}

func (suite *SubcommandTestSuite) assertReceipt(driver, archive string, verified bool) {
	r := suite.readReceipt(driver)
	suite.Equal(testArchiveSHA256(suite, archive), r.ArchiveSHA256)
	suite.Equal(verified, *r.SignatureVerified)
}

func (suite *SubcommandTestSuite) TestInstallReceiptEvidence() {
	suite.runCmd(InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.tar.gz"))))
	suite.assertReceipt("test-driver-1", "test-driver-1.tar.gz", true)
}

func (suite *SubcommandTestSuite) TestInstallNoVerifyReceiptEvidence() {
	suite.runCmd(InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, NoVerify: true}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.tar.gz"))))
	suite.assertReceipt("test-driver-1", "test-driver-1.tar.gz", false)
}

func (suite *SubcommandTestSuite) syncReceiptEvidence(noVerify, verified bool) {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, NoVerify: noVerify}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.tar.gz"))))
	suite.assertReceipt("test-driver-1", "test-driver-1.tar.gz", verified)
}

func (suite *SubcommandTestSuite) TestSyncReceiptEvidence() {
	suite.syncReceiptEvidence(false, true)
}

func (suite *SubcommandTestSuite) TestSyncNoVerifyReceiptEvidence() {
	suite.syncReceiptEvidence(true, false)
}

// upgradeRegistry serves v1.0.0 and v1.1.0 of test-driver-1, each with its
// own published archive hash.
func upgradeRegistry(hash10, hash11 string) func() ([]dbc.Driver, error) {
	return func() ([]dbc.Driver, error) {
		index := fmt.Sprintf(`drivers:
  - name: Test Driver 1
    description: This is a test driver
    license: MIT
    path: test-driver-1
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %[1]s
            url: test-driver-1/1.0.0/test-driver-1.tar.gz
            hash: sha256:%[2]s
      - version: v1.1.0
        packages:
          - platform: %[1]s
            url: test-driver-1/1.1.0/test-driver-1.1.tar.gz
            hash: sha256:%[3]s
`, config.PlatformTuple(), hash10, hash11)
		var decoded struct {
			Drivers []dbc.Driver `yaml:"drivers"`
		}
		if err := yaml.Unmarshal([]byte(index), &decoded); err != nil {
			return nil, err
		}
		for i := range decoded.Drivers {
			decoded.Drivers[i].Registry = &testRegistry
		}
		return decoded.Drivers, nil
	}
}

// assertFailedUpgradeKeepsInstall installs v1.0.0, then attempts an update to
// v1.1.0 using the given model and expects it to fail without touching the
// existing receipt, registration or library bytes.
func (suite *SubcommandTestSuite) assertFailedUpgradeKeepsInstall(upgrade InstallCmd, base baseModel) {
	suite.runCmd(InstallCmd{Driver: "test-driver-1=1.0.0", Level: suite.configLevel}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.tar.gz"))))
	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)

	shared := suite.getInstalledDriver("test-driver-1").Driver.Shared.Get(config.PlatformTuple())
	oldReceipt, err := os.ReadFile(suite.receiptPath("test-driver-1"))
	suite.Require().NoError(err)
	oldLib, err := os.ReadFile(shared)
	suite.Require().NoError(err)

	upgrade.Level = suite.configLevel
	suite.runCmdErr(upgrade.GetModelCustom(base))

	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)
	suite.Equal(shared, suite.getInstalledDriver("test-driver-1").Driver.Shared.Get(config.PlatformTuple()))
	newReceipt, err := os.ReadFile(suite.receiptPath("test-driver-1"))
	suite.Require().NoError(err)
	suite.Equal(oldReceipt, newReceipt)
	newLib, err := os.ReadFile(shared)
	suite.Require().NoError(err)
	suite.Equal(oldLib, newLib)
}

func (suite *SubcommandTestSuite) TestFailedHashUpgradeKeepsExistingInstall() {
	suite.assertFailedUpgradeKeepsInstall(
		InstallCmd{Driver: "test-driver-1=1.1.0"},
		baseModel{
			getDriverRegistry: upgradeRegistry(testArchiveSHA256(suite, "test-driver-1.tar.gz"), strings.Repeat("0", 64)),
			downloadPkg:       downloadTestPkg,
		})
}

func (suite *SubcommandTestSuite) TestFailedSignatureUpgradeKeepsExistingInstall() {
	tampered := testArchiveSHA256(suite, "test-driver-tampered.tar.gz")
	suite.assertFailedUpgradeKeepsInstall(
		InstallCmd{Driver: "test-driver-1=1.1.0"},
		baseModel{
			getDriverRegistry: upgradeRegistry(testArchiveSHA256(suite, "test-driver-1.tar.gz"), tampered),
			downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
				if pkg.Version.Minor() == 1 {
					return os.Open(filepath.Join("testdata", "test-driver-tampered.tar.gz"))
				}
				return downloadTestPkg(pkg)
			},
		})
}
