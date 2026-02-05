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
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc"
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
			baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

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
			baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

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
			baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		var err error
		m, err = p.Run()
		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())
		assert.Contains(t, out.String(), "old constraint: any; new constraint: >=1.0.0")

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
				baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

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
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

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
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

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
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	suite.Contains(out, "driver `test-driver-only-pre` not found in driver registry index")
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
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

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
		baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

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
