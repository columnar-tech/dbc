// Copyright 2025 Columnar Technologies Inc.
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "dbc.toml")

	// Create the file to simulate it already existing
	require.NoError(t, os.WriteFile(filePath, []byte("existing content"), 0644))
	t.Cleanup(func() {
		os.Remove(filePath)
	})
	m := InitCmd{Path: filePath}.GetModel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithContext(ctx))

	_, err := p.Run()
	require.NoError(t, err)
	assert.Contains(t, out.String(), "dbc.toml already exists")
}

func TestInit(t *testing.T) {
	dir := t.TempDir()

	custom := filepath.Join(dir, "custom.toml")
	cur, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		os.Chdir(cur)
	})

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

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
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
