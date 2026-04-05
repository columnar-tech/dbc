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
	"fmt"
	"os"
	"path/filepath"

	"github.com/columnar-tech/dbc"
)

func (suite *SubcommandTestSuite) TestSync() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0 already installed\r\n\rDone!\r\n", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSyncWithVersion() {
	tests := []struct {
		driver          string
		expectedVersion string
	}{
		{"test-driver-1=1.0.0", "1.0.0"},
		{"test-driver-1<=1.0.0", "1.0.0"},
		{"test-driver-1<1.1.0", "1.0.0"},
		{"test-driver-1~1.0", "1.0.0"},
		{"test-driver-1^1.0", "1.1.0"},
	}

	for _, tt := range tests {
		suite.Run(tt.driver, func() {
			m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
			suite.runCmd(m)

			m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{tt.driver}}.GetModel()
			suite.runCmd(m)

			m = SyncCmd{
				Path: filepath.Join(suite.tempdir, "dbc.toml"),
			}.GetModelCustom(
				baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
			suite.validateOutput("✓ test-driver-1-"+tt.expectedVersion+"\r\n\rDone!\r\n", "", suite.runCmd(m))
			suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
			suite.FileExists(filepath.Join(suite.tempdir, "dbc.lock"))

			for _, f := range suite.getFilesInTempDir() {
				os.Remove(filepath.Join(suite.tempdir, f))
			}
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncVirtualEnv() {
	suite.T().Setenv("ADBC_DRIVER_PATH", "")

	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	suite.T().Setenv("VIRTUAL_ENV", suite.tempdir)

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "etc", "adbc", "drivers", "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0 already installed\r\n\rDone!\r\n", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSyncCondaPrefix() {
	suite.T().Setenv("ADBC_DRIVER_PATH", "")

	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	suite.T().Setenv("CONDA_PREFIX", suite.tempdir)

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "etc", "adbc", "drivers", "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-1-1.1.0 already installed\r\n\rDone!\r\n", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSyncInstallFailSig() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-no-sig"}}.GetModel()
	suite.runCmd(m)

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r ",
		"\nError: failed to verify signature: signature file 'test-driver-1-not-valid.so.sig' for driver is missing",
		suite.runCmdErr(m))
	suite.Equal([]string{"dbc.toml"}, suite.getFilesInTempDir())
}

func (suite *SubcommandTestSuite) TestSyncInstallNoVerify() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-no-sig"}}.GetModel()
	suite.runCmd(m)

	m = SyncCmd{
		Path:     filepath.Join(suite.tempdir, "dbc.toml"),
		NoVerify: true,
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("✓ test-driver-no-sig-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSyncPartialRegistryFailure() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	// Test that sync command handles partial registry failure gracefully
	// (one registry succeeds, another fails - returns both drivers and error)
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://backup-registry.example.com: failed to fetch driver registry: network timeout")
	}

	// Should succeed if the requested driver is found in the available drivers
	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})

	// Should install successfully without printing the registry error
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestSyncPartialRegistryFailureDriverNotFound() {
	// Initialize driver list with a driver that doesn't exist
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Manually create a driver list with a nonexistent driver
	err := os.WriteFile(filepath.Join(suite.tempdir, "dbc.toml"), []byte(`# dbc driver list
[drivers]
[drivers.nonexistent-driver]
`), 0644)
	suite.Require().NoError(err)

	// Test that sync command shows registry errors when the requested driver is not found
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://backup-registry.example.com: failed to fetch driver registry: network timeout")
	}

	// Should fail with enhanced error message if the requested driver is not found
	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	// Should show the driver not found error AND the registry error
	suite.Contains(out, "driver `nonexistent-driver` not found")
	suite.Contains(out, "Note: Some driver registries were unavailable")
	suite.Contains(out, "failed to fetch driver registry")
	suite.Contains(out, "network timeout")
}

func (suite *SubcommandTestSuite) TestSyncCompleteRegistryFailure() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	// Test that sync command handles complete registry failure (no drivers returned)
	completeFailingRegistry := func() ([]dbc.Driver, error) {
		return nil, fmt.Errorf("registry https://primary-registry.example.com: connection refused")
	}

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: completeFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	suite.Contains(out, "connection refused")
}
