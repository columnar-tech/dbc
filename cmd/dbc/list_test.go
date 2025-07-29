// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getTestDriverList() ([]dbc.Driver, error) {
	return []dbc.Driver{
		{
			Title:   "Test Driver 1",
			Desc:    "This is a test driver",
			License: "MIT",
			Path:    "test-driver-1",
		},
		{
			Title:   "Test Driver 2",
			Desc:    "This is another test driver",
			License: "Apache-2.0",
			Path:    "test-driver-2",
		},
	}, nil
}

func TestOutput(t *testing.T) {
	defer func(fn func() ([]dbc.Driver, error)) {
		getDriverList = fn
	}(getDriverList)
	getDriverList = getTestDriverList

	const (
		terminalPrefix = "\x1b[?25l\x1b[?2004h"
		terminalSuffix = " \x1b[D\x1b[2K\r\x1b[?2004l\x1b[?25h\x1b[?1002l\x1b[?1003l\x1b[?1006l"
	)

	tests := []struct {
		name     string
		cmd      modelCmd
		expected string
	}{
		{"list", ListCmd{Verbose: false}, "Current System: linux_amd64\r\n" +
			"\r\n• test-driver-1\r\n   This is a test driver\r\n" +
			"• test-driver-2\r\n   This is another test driver\r\n\r"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			p := tea.NewProgram(tt.cmd.GetModel(), tea.WithInput(nil), tea.WithOutput(&out),
				tea.WithContext(ctx))

			_, err := p.Run()
			require.NoError(t, err)
			assert.Equal(t, terminalPrefix+tt.expected+terminalSuffix, out.String())
		})
	}
}
