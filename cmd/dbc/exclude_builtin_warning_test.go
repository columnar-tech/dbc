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
	"os"
	"path/filepath"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/stretchr/testify/assert"
)

const ineffectiveExcludeWarning = "exclude_builtin_registries = false in dbc.toml has no effect"

func TestProjectRegistryWarning(t *testing.T) {
	excluding := &dbc.GlobalConfig{ExcludeBuiltinRegistries: true}
	for _, tc := range []struct {
		name    string
		global  *dbc.GlobalConfig
		project *bool
		baseURL string
		want    bool
	}{
		{name: "explicit false while the user config excludes", global: excluding, project: new(false), want: true},
		{name: "omitted", global: excluding},
		{name: "true", global: excluding, project: new(true)},
		{name: "user config keeps the built-in registries", global: &dbc.GlobalConfig{}, project: new(false)},
		{name: "no user config", project: new(false)},
		{name: "DBC_BASE_URL replaces the registry configuration", global: excluding, project: new(false), baseURL: "https://base.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DBC_BASE_URL", tc.baseURL)
			saved := globalRegistryConfig
			t.Cleanup(func() { globalRegistryConfig = saved })
			globalRegistryConfig = tc.global

			got := projectRegistryWarning(DriversList{ExcludeBuiltinRegistries: tc.project})
			if tc.want {
				assert.Contains(t, got, ineffectiveExcludeWarning)
			} else {
				assert.Empty(t, got)
			}
		})
	}
}

func (suite *SubcommandTestSuite) withExcludingUserConfig() {
	saved := globalRegistryConfig
	suite.T().Cleanup(func() { globalRegistryConfig = saved })
	globalRegistryConfig = &dbc.GlobalConfig{
		Registries:               []dbc.RegistryEntry{{URL: "https://user.example.com"}},
		ExcludeBuiltinRegistries: true,
	}
	suite.T().Setenv("DBC_BASE_URL", "")
}

func (suite *SubcommandTestSuite) TestSyncWarnsAboutIneffectiveExcludeFalse() {
	suite.withExcludingUserConfig()
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath,
		[]byte("exclude_builtin_registries = false\n\n[drivers]\n[drivers.test-driver-1]\n"), 0o644))

	var out string
	stderr := captureStderr(suite, func() {
		out = suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(testBaseModel()))
	})
	suite.Contains(stderr, ineffectiveExcludeWarning)
	suite.NotContains(out, ineffectiveExcludeWarning)
	suite.driverIsInstalled("test-driver-1", true)
}

func (suite *SubcommandTestSuite) TestAddKeepsExplicitExcludeFalse() {
	suite.withExcludingUserConfig()
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath,
		[]byte("exclude_builtin_registries = false\n\n[drivers]\n"), 0o644))

	stderr := captureStderr(suite, func() {
		suite.runCmd(AddCmd{Path: listPath, Driver: []string{"test-driver-1"}}.GetModelCustom(testBaseModel()))
	})
	suite.Contains(stderr, ineffectiveExcludeWarning)

	data, err := os.ReadFile(listPath)
	suite.Require().NoError(err)
	suite.Contains(string(data), "exclude_builtin_registries = false")
	suite.Contains(string(data), "[drivers.test-driver-1]")
}

func (suite *SubcommandTestSuite) TestProjectRegistriesFromCWDWarnAboutIneffectiveExcludeFalse() {
	suite.withExcludingUserConfig()
	suite.Require().NoError(os.WriteFile(filepath.Join(suite.tempdir, "dbc.toml"),
		[]byte("exclude_builtin_registries = false\n\n[drivers]\n"), 0o644))
	suite.T().Chdir(suite.tempdir)

	stderr := captureStderr(suite, func() {
		suite.Require().NoError(applyProjectRegistriesFromCWD())
	})
	suite.Contains(stderr, ineffectiveExcludeWarning)
}
