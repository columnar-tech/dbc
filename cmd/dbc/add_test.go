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
