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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInit(t *testing.T) {
	dir := t.TempDir()

	custom := filepath.Join(dir, "custom.toml")
	require.NoError(t, os.Chdir(dir))

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{"default", ".", "./dbc.toml"},
		{"custom arg", custom, custom},
		{"custom dir", ".", filepath.Join(dir, "dbc.toml")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := InitCmd{Path: tt.path}.GetModel()

			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
			defer cancel()

			var out bytes.Buffer
			p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
				tea.WithContext(ctx))

			var err error
			m, err = p.Run()

			require.NoError(t, err)
			assert.Equal(t, 0, m.(HasStatus).Status())

			assert.FileExists(t, tt.expected)
			os.Remove(tt.expected)
		})
	}
}
