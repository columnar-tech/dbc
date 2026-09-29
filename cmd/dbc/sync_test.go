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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

func (suite *SubcommandTestSuite) TestSync() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	downloads := 0
	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{
			getDriverRegistry: getTestDriverRegistry,
			downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
				downloads++
				return downloadTestPkg(pkg)
			},
		})
	suite.validateOutput("✓ test-driver-1-1.1.0 already installed\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.Zero(downloads, "same-version sync should not download the package")
}

func (suite *SubcommandTestSuite) TestSyncReplacementSignatureFailurePreservesInstalledDriver() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))

	m := SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(testBaseModel())
	suite.runCmd(m)
	old := suite.getInstalledDriver("test-driver-1")
	var oldManifest []byte
	if runtime.GOOS != "windows" || suite.configLevel == config.ConfigEnv {
		var err error
		oldManifest, err = os.ReadFile(filepath.Join(suite.Dir(), "test-driver-1.toml"))
		suite.Require().NoError(err)
	}
	oldLibrary := old.Driver.Shared.Get(config.PlatformTuple())
	oldLibraryBytes, err := os.ReadFile(oldLibrary)
	suite.Require().NoError(err)

	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\nversion = '=1.0.0'\n"), 0o644))
	model := SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(dbc.PkgInfo) (*os.File, error) {
			return os.Open(filepath.Join("testdata", "test-driver-no-sig.tar.gz"))
		},
	})
	suite.Contains(suite.runCmdErr(model), "failed to verify signature: verify package: signature file '")

	current := suite.getInstalledDriver("test-driver-1")
	suite.Equal(old.Version, current.Version)
	suite.Equal(old.Driver.Shared.Get(config.PlatformTuple()), current.Driver.Shared.Get(config.PlatformTuple()))
	if runtime.GOOS != "windows" || suite.configLevel == config.ConfigEnv {
		currentManifest, err := os.ReadFile(filepath.Join(suite.Dir(), "test-driver-1.toml"))
		suite.Require().NoError(err)
		suite.Equal(oldManifest, currentManifest)
	}
	currentLibraryBytes, err := os.ReadFile(current.Driver.Shared.Get(config.PlatformTuple()))
	suite.Require().NoError(err)
	suite.Equal(oldLibraryBytes, currentLibraryBytes)
}

func (suite *SubcommandTestSuite) TestSyncReplacementUsesTransactionAndShadowsSecondaryRoot() {
	secondary := filepath.Join(suite.tempdir, "secondary")
	suite.Require().NoError(os.MkdirAll(secondary, 0o755))
	suite.T().Setenv("ADBC_DRIVER_PATH", secondary)
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))

	m := SyncCmd{Path: listPath, Level: config.ConfigEnv}.GetModelCustom(testBaseModel())
	suite.runCmd(m)
	secondaryDriver, err := config.GetDriver(config.Get()[config.ConfigEnv], "test-driver-1")
	suite.Require().NoError(err)
	secondaryLibrary := secondaryDriver.Driver.Shared.Get(config.PlatformTuple())
	secondaryBytes, err := os.ReadFile(secondaryLibrary)
	suite.Require().NoError(err)

	suite.T().Setenv("ADBC_DRIVER_PATH", suite.tempdir+string(os.PathListSeparator)+secondary)
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\nversion = '=1.0.0'\n"), 0o644))
	m = SyncCmd{Path: listPath, Level: config.ConfigEnv}.GetModelCustom(testBaseModel())
	out := suite.runCmd(m)
	suite.NotContains(out, "removed")

	primaryDriver, err := config.GetDriver(config.Get()[config.ConfigEnv], "test-driver-1")
	suite.Require().NoError(err)
	suite.Equal("1.0.0", primaryDriver.Version.String())
	primaryLibrary := primaryDriver.Driver.Shared.Get(config.PlatformTuple())
	suite.True(strings.HasPrefix(filepath.Base(filepath.Dir(primaryLibrary)), fmt.Sprintf(".dbc-package-g-%d-test-driver-1-", len([]byte("test-driver-1")))))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
	suite.FileExists(primaryLibrary)
	primaryRel, err := filepath.Rel(suite.tempdir, primaryLibrary)
	suite.Require().NoError(err)
	suite.False(filepath.IsAbs(primaryRel) || primaryRel == ".." || strings.HasPrefix(primaryRel, ".."+string(os.PathSeparator)),
		"primary library should be inside the primary root: %s", primaryLibrary)
	remainingSecondaryBytes, err := os.ReadFile(secondaryLibrary)
	suite.Require().NoError(err)
	suite.Equal(secondaryBytes, remainingSecondaryBytes, "secondary-root library should remain intact when shadowed")
	suite.FileExists(filepath.Join(secondary, "test-driver-1.toml"))

	lock, err := loadLockFile(filepath.Join(suite.tempdir, "dbc.lock"))
	suite.Require().NoError(err)
	lockInfo := lock.lockinfo["test-driver-1"]
	suite.Equal("1.0.0", lockInfo.Version.String())
	primaryChecksum, err := checksum(primaryLibrary)
	suite.Require().NoError(err)
	suite.Equal(primaryChecksum, lockInfo.Checksum)
}

func (suite *SubcommandTestSuite) TestSyncPrimaryVersionReplacementUpdatesRegistrationAndLockChecksum() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))

	m := SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(testBaseModel())
	suite.runCmd(m)
	old := suite.getInstalledDriver("test-driver-1")
	oldLibrary := old.Driver.Shared.Get(config.PlatformTuple())

	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\nversion = '=1.0.0'\n"), 0o644))
	m = SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(testBaseModel())
	out := suite.runCmd(m)
	suite.NotContains(out, "removed")

	current := suite.getInstalledDriver("test-driver-1")
	currentLibrary := current.Driver.Shared.Get(config.PlatformTuple())
	suite.Equal("1.0.0", current.Version.String())
	suite.NotEqual(oldLibrary, currentLibrary)
	suite.True(strings.HasPrefix(filepath.Base(filepath.Dir(currentLibrary)), fmt.Sprintf(".dbc-package-g-%d-test-driver-1-", len([]byte("test-driver-1")))))
	suite.FileExists(currentLibrary)

	lock, err := loadLockFile(filepath.Join(suite.tempdir, "dbc.lock"))
	suite.Require().NoError(err)
	lockInfo := lock.lockinfo["test-driver-1"]
	suite.Equal("1.0.0", lockInfo.Version.String())
	currentChecksum, err := checksum(currentLibrary)
	suite.Require().NoError(err)
	suite.Equal(currentChecksum, lockInfo.Checksum)
}

func (suite *SubcommandTestSuite) TestSyncRejectsUnsupportedLockFileWithoutRewriting() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	lockContents := []byte("version = 2\nrevision = 0\n" +
		"[[drivers]]\nname = 'test-driver-1'\nversion = '1.0.0'\n" +
		"[drivers.source]\ntype = 'packslip'\n" +
		"[[drivers.artifacts]]\nformat = 'tar.gz'\n")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	suite.Require().NoError(os.WriteFile(lockPath, lockContents, 0o644))

	m := SyncCmd{Path: listPath}.GetModelCustom(testBaseModel())
	output := suite.runCmdErr(m)
	suite.Contains(output, "unsupported lock file version 2")

	actual, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(lockContents, actual)
	suite.NoFileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
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
				testBaseModel())
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
		testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "etc", "adbc", "drivers", "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		testBaseModel())
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
		testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "etc", "adbc", "drivers", "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		testBaseModel())
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
		testBaseModel())
	suite.validateOutput("\r ",
		"\nError: failed to verify signature: verify package: signature file 'test-driver-1-not-valid.so.sig' for driver is missing",
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
		testBaseModel())
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

func (suite *SubcommandTestSuite) TestSyncWithProjectRegistries() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	err := os.WriteFile(filepath.Join(suite.tempdir, "dbc.toml"), []byte(`# dbc driver list
[[registries]]
url = 'https://custom-registry.example.com'
name = 'custom'

[drivers]
[drivers.test-driver-1]
`), 0644)
	suite.Require().NoError(err)

	m = SyncCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModelCustom(testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	if os.Getenv("DBC_BASE_URL") == "" {
		suite.Require().NotNil(dbcClient)
		found := false
		for _, r := range dbcClient.Registries() {
			if r.BaseURL != nil && r.BaseURL.String() == "https://custom-registry.example.com" {
				found = true
				break
			}
		}
		suite.True(found, "expected custom registry in active client registries after sync with [[registries]] in dbc.toml")
	}
}

func (suite *SubcommandTestSuite) TestSyncWithProjectRegistriesBackwardCompat() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	m = SyncCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModelCustom(testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
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

func (suite *SubcommandTestSuite) TestSync_JSONStream() {
	tmpDir := suite.T().TempDir()
	driverListPath := filepath.Join(tmpDir, "dbc.toml")
	err := os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0644)
	suite.Require().NoError(err)

	m := SyncCmd{Path: driverListPath, Json: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	suite.Greater(len(lines), 0, "expected at least one NDJSON line")

	var lastEnv jsonschema.Envelope
	for _, line := range lines {
		if line == "" {
			continue
		}
		var env jsonschema.Envelope
		suite.Require().NoError(json.Unmarshal([]byte(line), &env), "line must be valid JSON: %s", line)
		suite.Equal(1, env.SchemaVersion)
		lastEnv = env
	}
	suite.Equal("sync.status", lastEnv.Kind)

	var status jsonschema.SyncStatus
	suite.Require().NoError(json.Unmarshal(lastEnv.Payload, &status))
	suite.Len(status.Installed, 1)
	suite.Equal("test-driver-1", status.Installed[0].Name)
}

func (suite *SubcommandTestSuite) TestSync_JSONProgressStream() {
	tmpDir := suite.T().TempDir()
	driverListPath := filepath.Join(tmpDir, "dbc.toml")
	err := os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0644)
	suite.Require().NoError(err)

	m := SyncCmd{Path: driverListPath, JsonStreamProgress: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	suite.Greater(len(lines), 1, "expected multiple NDJSON lines")

	var kinds []string
	for _, line := range lines {
		if line == "" {
			continue
		}
		var env jsonschema.Envelope
		suite.Require().NoError(json.Unmarshal([]byte(line), &env), "line must be valid JSON: %s", line)
		suite.Equal(1, env.SchemaVersion)
		kinds = append(kinds, env.Kind)
	}

	suite.Contains(kinds, "sync.progress")
	suite.Equal("sync.status", kinds[len(kinds)-1])
}
