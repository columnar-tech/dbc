// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdd(t *testing.T) {
	dir := t.TempDir()

	cur, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		os.Chdir(cur)
	})

	{
		m := InitCmd{Path: "./dbc.toml"}.GetModel()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		m, err = p.Run()

		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		assert.FileExists(t, "./dbc.toml")
	}

	{
		m := AddCmd{Path: "./dbc.toml", Driver: "foobar"}.GetModel()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
			tea.WithContext(ctx))

		var err error
		m, err = p.Run()

		require.NoError(t, err)
		assert.Equal(t, 0, m.(HasStatus).Status())

		data, err := os.ReadFile("./dbc.toml")
		require.NoError(t, err)
		assert.Equal(t, `# dbc driver list
[drivers]
[drivers.foobar]
`, string(data))
	}
}
