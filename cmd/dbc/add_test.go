// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

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
	defer func(fn func() ([]dbc.Driver, error)) {
		getDriverList = fn
	}(getDriverList)
	getDriverList = getTestDriverList

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
		m := AddCmd{Path: filepath.Join(dir, "dbc.toml"), Driver: "test-driver-1"}.GetModel()

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
