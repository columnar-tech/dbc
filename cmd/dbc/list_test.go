// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func getTestDriverList() ([]dbc.Driver, error) {
	drivers := struct {
		Drivers []dbc.Driver `yaml:"drivers"`
	}{}

	f, err := os.Open("testdata/test_manifest.yaml")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return drivers.Drivers, yaml.NewDecoder(f).Decode(&drivers)
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
		{"list", ListCmd{Verbose: false}, "Current System: " + platformTuple + "\r\n" +
			"\r\n• test-driver-1 - This is a test driver\r\n" +
			"• test-driver-2 - This is another test driver\r\n\r"},
		{"list verbose", ListCmd{Verbose: true}, "Current System: " + platformTuple + "\r\n" +
			"\r\n• test-driver-1\r\n   Title: Test Driver 1\r\n   Description: This is a test driver\r\n" +
			"   License: MIT\r\n   Versions:\r\n" +
			"    ├── 1.0.0\r\n" +
			"    ╰── 1.1.0\r\n" +
			"• test-driver-2\r\n   Title: Test Driver 2\r\n   Description: This is another test driver\r\n" +
			"   License: Apache-2.0\r\n   Versions:\r\n" +
			"    ├── 2.0.0\r\n" +
			"    ╰── 2.1.0\r\n\r"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			p := tea.NewProgram(tt.cmd.GetModel(),
				tea.WithInput(nil), tea.WithOutput(&out),
				tea.WithContext(ctx), tea.WithEnvironment(append(os.Environ(), "TERM=xterm")))

			_, err := p.Run()
			require.NoError(t, err)
			assert.Equal(t, terminalPrefix+tt.expected+terminalSuffix, out.String())
		})
	}
}
