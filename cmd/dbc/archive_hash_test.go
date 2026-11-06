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
	"fmt"
	"os"
	"path/filepath"

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
	suite.Contains(out, "does not match the registry")
	suite.Contains(out, zeros)
	suite.Contains(out, testArchiveSHA256(suite, "test-driver-1.tar.gz"))
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestInstallRegistryHashMismatchJSON() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Json: true}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.1.tar.gz")))
	out := suite.runCmdErr(m)
	suite.Contains(out, "does not match the registry")
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestInstallRegistryHashMismatchWithNoVerify() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, NoVerify: true}.
		GetModelCustom(hashedBaseModel(testArchiveSHA256(suite, "test-driver-1.1.tar.gz")))
	suite.Contains(suite.runCmdErr(m), "does not match the registry")
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
	suite.Contains(out, "does not match the registry")
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
