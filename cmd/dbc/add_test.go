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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdd(t *testing.T) {
	dir := t.TempDir()
	var err error
	{
		m := InitCmd{Path: filepath.Join(dir, "dbc.toml")}.GetModel()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		m, err = p.Run()

		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		assert.FileExists(t, filepath.Join(dir, "dbc.toml"))
	}

	{
		m := AddCmd{Path: filepath.Join(dir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModelCustom(
			testBaseModel())

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		var err error
		m, err = p.Run()
		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		data, err := os.ReadFile(filepath.Join(dir, "dbc.toml"))
		require.NoError(t, err)
		assert.Equal(t, `# dbc driver list
[drivers]
[drivers.test-driver-1]
`, string(data))
	}
}

func TestAddRepeatedNewWithConstraint(t *testing.T) {
	// Test what happens when we `add` without a constraint and then add with a
	// constraint. This specifically tests the bubbletea output
	defer func(fn func() ([]dbc.Driver, error)) {
		getDriverRegistry = fn
	}(getDriverRegistry)
	getDriverRegistry = getTestDriverRegistry

	dir := t.TempDir()
	var err error
	{
		m := InitCmd{Path: filepath.Join(dir, "dbc.toml")}.GetModel()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		m, err = p.Run()

		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		assert.FileExists(t, filepath.Join(dir, "dbc.toml"))
	}

	{
		m := AddCmd{Path: filepath.Join(dir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModelCustom(
			testBaseModel())

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		var err error
		m, err = p.Run()
		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		data, err := os.ReadFile(filepath.Join(dir, "dbc.toml"))
		require.NoError(t, err)
		assert.Equal(t, `# dbc driver list
[drivers]
[drivers.test-driver-1]
`, string(data))
	}

	{
		m := AddCmd{Path: filepath.Join(dir, "dbc.toml"), Driver: []string{"test-driver-1>=1.0.0"}}.GetModelCustom(
			testBaseModel())

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		var err error
		m, err = p.Run()
		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())
		if fo, ok := m.(HasFinalOutput); ok {
			assert.Contains(t, fo.FinalOutput(), "old constraint: any; new constraint: >=1.0.0")
		}

		data, err := os.ReadFile(filepath.Join(dir, "dbc.toml"))
		require.NoError(t, err)
		assert.Equal(t, `# dbc driver list
[drivers]
[drivers.test-driver-1]
version = '>=1.0.0'
`, string(data))
	}
}

func TestAddMultiple(t *testing.T) {
	// Test what happens when we `add` without a constraint and then add with a
	// constraint. This specifically tests the bubbletea output
	defer func(fn func() ([]dbc.Driver, error)) {
		getDriverRegistry = fn
	}(getDriverRegistry)
	getDriverRegistry = getTestDriverRegistry

	dir := t.TempDir()
	var err error
	{
		m := InitCmd{Path: filepath.Join(dir, "dbc.toml")}.GetModel()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		m, err = p.Run()

		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		assert.FileExists(t, filepath.Join(dir, "dbc.toml"))
	}
	{
		m := AddCmd{Path: filepath.Join(dir, "dbc.toml"), Driver: []string{"test-driver-2", "test-driver-1>=1.0.0"}}.
			GetModelCustom(
				testBaseModel())

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		var err error
		m, err = p.Run()
		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		data, err := os.ReadFile(filepath.Join(dir, "dbc.toml"))
		require.NoError(t, err)
		assert.Equal(t, `# dbc driver list
[drivers]
[drivers.test-driver-1]
version = '>=1.0.0'

[drivers.test-driver-2]
`, string(data))
	}
}

func (suite *SubcommandTestSuite) TestAddWithPre() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add driver with --pre flag
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-2"},
		Pre:    true,
	}.GetModelCustom(
		testBaseModel())

	suite.runCmd(m)

	// Verify the file contents
	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Equal(`# dbc driver list
[drivers]
[drivers.test-driver-2]
prerelease = 'allow'
`, string(data))
}

func (suite *SubcommandTestSuite) TestAddWithPreOnlyPrereleaseDriver() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add driver that only has prerelease versions with --pre flag
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-only-pre"},
		Pre:    true,
	}.GetModelCustom(
		testBaseModel())

	suite.runCmd(m)

	// Verify the file contents
	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Equal(`# dbc driver list
[drivers]
[drivers.test-driver-only-pre]
prerelease = 'allow'
`, string(data))
}

func (suite *SubcommandTestSuite) TestAddWithoutPreOnlyPrereleaseDriver() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Try to add driver that only has prerelease versions without --pre flag (should fail)
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-only-pre"},
		Pre:    false,
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmdErr(m)
	suite.Contains(out, "driver `test-driver-only-pre` not found in driver registry index (but prerelease versions filtered out); try: dbc add --pre test-driver-only-pre")
}

func (suite *SubcommandTestSuite) TestAddWithPreAndConstraint() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add driver with --pre flag and a version constraint
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-2>=2.0.0"},
		Pre:    true,
	}.GetModelCustom(
		testBaseModel())

	suite.runCmd(m)

	// Verify the file contents
	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Equal(`# dbc driver list
[drivers]
[drivers.test-driver-2]
prerelease = 'allow'
version = '>=2.0.0'
`, string(data))
}

func (suite *SubcommandTestSuite) TestAddExplicitPrereleaseWithoutPreFlag() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add explicit prerelease version WITHOUT --pre flag, should succeed per requirement
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-only-pre=0.9.0-alpha.1"},
		Pre:    false,
	}.GetModelCustom(
		testBaseModel())

	suite.runCmd(m)

	// Verify the file contents - should NOT include prerelease = 'allow' since --pre was not specified
	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Equal(`# dbc driver list
[drivers]
[drivers.test-driver-only-pre]
version = '=0.9.0-alpha.1'
`, string(data))
}

func (suite *SubcommandTestSuite) TestAddPartialRegistryFailure() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Test that add command handles partial registry failure gracefully
	// (one registry succeeds, another fails - returns both drivers and error)
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://cdn-fallback.example.com: failed to fetch driver registry: DNS resolution failed")
	}

	// Should succeed if the requested driver is found in the available drivers
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
		Pre:    false,
	}.GetModelCustom(
		baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})

	suite.runCmd(m)
	// Should succeed without printing the registry error

	// Verify the file was updated correctly
	data, err := os.ReadFile(filepath.Join(suite.tempdir, "dbc.toml"))
	suite.Require().NoError(err)
	suite.Contains(string(data), "[drivers.test-driver-1]")
}

func (suite *SubcommandTestSuite) TestAddPartialRegistryFailureDriverNotFound() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Test that add command shows registry errors when the requested driver is not found
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://cdn-fallback.example.com: failed to fetch driver registry: DNS resolution failed")
	}

	// Should fail with enhanced error message if the requested driver is not found
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"nonexistent-driver"},
		Pre:    false,
	}.GetModelCustom(
		baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	// Should show the driver not found error AND the registry error
	suite.Contains(out, "driver `nonexistent-driver` not found")
	suite.Contains(out, "Note: Some driver registries were unavailable")
	suite.Contains(out, "failed to fetch driver registry")
	suite.Contains(out, "DNS resolution failed")
}

func (suite *SubcommandTestSuite) TestAddCompleteRegistryFailure() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Test that add command handles complete registry failure (no drivers returned)
	completeFailingRegistry := func() ([]dbc.Driver, error) {
		return nil, fmt.Errorf("registry https://primary-cdn.example.com: network unreachable")
	}

	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
		Pre:    false,
	}.GetModelCustom(
		baseModel{getDriverRegistry: completeFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	suite.Contains(out, "error getting driver list")
	suite.Contains(out, "network unreachable")
}

func (suite *SubcommandTestSuite) TestAddOutput() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmd(m)
	suite.Contains(out, "added test-driver-1 to driver list")
	suite.Contains(out, "use `dbc sync` to install the drivers in the list")
}

func (suite *SubcommandTestSuite) TestAdd_JSON() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
		Json:   true,
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmd(m)

	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(out), &env), "output must be valid JSON: %s", out)
	suite.Equal(1, env.SchemaVersion)
	suite.Equal("add.response", env.Kind)

	var resp jsonschema.AddResponse
	suite.Require().NoError(json.Unmarshal(env.Payload, &resp))
	suite.Require().Len(resp.Drivers, 1)
	suite.Equal("test-driver-1", resp.Drivers[0].Name)
	suite.NotEmpty(resp.DriverListPath)
}

func (suite *SubcommandTestSuite) TestAdd_JSON_Constraint() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1>=1.0.0"},
		Json:   true,
	}.GetModelCustom(
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmd(m)

	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(out), &env), "output must be valid JSON: %s", out)
	suite.Equal(1, env.SchemaVersion)
	suite.Equal("add.response", env.Kind)

	// verify > is not HTML-escaped (>) in the JSON output
	suite.NotContains(string(env.Payload), "\\u003e")

	var resp jsonschema.AddResponse
	suite.Require().NoError(json.Unmarshal(env.Payload, &resp))
	suite.Require().Len(resp.Drivers, 1)
	suite.Equal("test-driver-1", resp.Drivers[0].Name)
	suite.Equal(">=1.0.0", resp.Drivers[0].VersionConstraint)
	suite.NotEmpty(resp.DriverListPath)
}

func (suite *SubcommandTestSuite) TestAddMultipleOutput() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1", "test-driver-2"},
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmd(m)
	suite.Contains(out, "added test-driver-1 to driver list")
	suite.Contains(out, "added test-driver-2 to driver list")
	suite.Contains(out, "use `dbc sync` to install the drivers in the list")
}

func (suite *SubcommandTestSuite) TestAddReplacingDriverOutput() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Add driver without constraint
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
	}.GetModelCustom(
		testBaseModel())
	suite.runCmd(m)

	// Add same driver with constraint and verify replacement message
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1>=1.0.0"},
	}.GetModelCustom(
		testBaseModel())

	out := suite.runCmd(m)
	suite.Contains(out, "replacing existing driver test-driver-1")
	suite.Contains(out, "old constraint: any; new constraint: >=1.0.0")
	suite.Contains(out, "added test-driver-1 to driver list")
	suite.Contains(out, "with constraint >=1.0.0")
}

func (suite *SubcommandTestSuite) TestAdd_JSON_DriverNotFound() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Use the real test registry but request a driver that doesn't exist,
	// exercising the findDriver path rather than the registry-failure path.
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"nonexistent-driver"},
		Json:   true,
	}.GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmdErr(m)
	suite.assertJSONErrorEnvelope(out, "add_failed")
}

func (suite *SubcommandTestSuite) TestAdd_JSON_RegistryFailure() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Verify that a complete registry failure also emits a structured error envelope
	// and that the underlying registry error detail is preserved in the message.
	failingRegistry := func() ([]dbc.Driver, error) {
		return nil, fmt.Errorf("network unreachable")
	}
	m = AddCmd{
		Path:   filepath.Join(suite.tempdir, "dbc.toml"),
		Driver: []string{"test-driver-1"},
		Json:   true,
	}.GetModelCustom(baseModel{getDriverRegistry: failingRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmdErr(m)
	suite.assertJSONErrorEnvelope(out, "add_failed", "network unreachable")
}
