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
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/alexflint/go-arg"
	"github.com/columnar-tech/dbc"
	"github.com/stretchr/testify/require"
)

func renderSubcommandHelp(t *testing.T, argv ...string) string {
	t.Helper()

	var args cmds
	p, err := newParser(&args)
	require.NoError(t, err)

	err = p.Parse(argv)
	require.ErrorIs(t, err, arg.ErrHelp)

	var out bytes.Buffer
	if d, ok := p.Subcommand().(arg.Described); ok {
		fmt.Fprintln(&out, d.Description())
	}
	p.WriteHelpForSubcommand(&out, p.SubcommandNames()...)
	return out.String()
}

func TestFormatErr(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantSubstring []string
	}{
		{
			name:          "ErrUnauthorized direct",
			err:           dbc.ErrUnauthorized,
			wantSubstring: []string{dbc.ErrUnauthorized.Error(), "Did you run `dbc auth login`?"},
		},
		{
			name:          "ErrUnauthorized wrapped",
			err:           fmt.Errorf("operation failed: %w", dbc.ErrUnauthorized),
			wantSubstring: []string{dbc.ErrUnauthorized.Error(), "Did you run `dbc auth login`?"},
		},
		{
			name:          "ErrUnauthorizedColumnar direct",
			err:           dbc.ErrUnauthorizedColumnar,
			wantSubstring: []string{dbc.ErrUnauthorizedColumnar.Error(), "active license", "support@columnar.tech"},
		},
		{
			name:          "ErrUnauthorizedColumnar wrapped",
			err:           fmt.Errorf("operation failed: %w", dbc.ErrUnauthorizedColumnar),
			wantSubstring: []string{dbc.ErrUnauthorizedColumnar.Error(), "active license", "support@columnar.tech"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatErr(tt.err)
			for _, want := range tt.wantSubstring {
				require.True(t, strings.Contains(got, want),
					"formatErr(%v) = %q, expected to contain %q", tt.err, got, want)
			}
		})
	}
}

func TestCmdStatus(t *testing.T) {
	tests := []struct {
		name   string
		cmd    modelCmd
		status int
	}{
		{"install",
			InstallCmd{Driver: "notfound"},
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			var in bytes.Buffer
			var out bytes.Buffer

			p := tea.NewProgram(tt.cmd.GetModel(), tea.WithInput(&in),
				tea.WithOutput(&out), tea.WithContext(ctx))

			var m tea.Model
			var err error
			done := make(chan struct{})
			go func() {
				m, err = p.Run()
				close(done)
			}()

			<-done

			require.NoError(t, err, out.String())

			if h, ok := m.(HasStatus); ok {
				require.Equal(t, tt.status, h.Status(), "name=%q: cmd=%#v", tt.name, tt.cmd)
			} else {
				t.Fatalf("model doesn't implement HasStatus")
			}
		})
	}
}

func TestInstallHelpMentionsVersionConstraints(t *testing.T) {
	out := renderSubcommandHelp(t, "install", "-h")

	require.Contains(t, out, "Driver to install, optionally with a version constraint")
	require.Contains(t, out, `dbc install "mysql=0.1.0"`)
	require.Contains(t, out, `dbc install "mysql>=1,<2"`)
	require.Contains(t, out, "https://docs.columnar.tech/dbc/guides/installing/#version-constraints")
}

func TestSubcommandSuggestions(t *testing.T) {
	tests := []struct {
		name            string
		invalidCmd      string
		wantSuggestion  string
		hasSuggestion   bool
	}{
		{
			name:           "list suggests search",
			invalidCmd:     "list",
			wantSuggestion: "search",
			hasSuggestion:  true,
		},
		{
			name:          "unknown command has no suggestion",
			invalidCmd:    "foobar",
			hasSuggestion: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suggestion, ok := subcommandSuggestions[tt.invalidCmd]
			require.Equal(t, tt.hasSuggestion, ok, "expected hasSuggestion=%v for command %q", tt.hasSuggestion, tt.invalidCmd)
			if tt.hasSuggestion {
				require.Equal(t, tt.wantSuggestion, suggestion, "wrong suggestion for command %q", tt.invalidCmd)
			}
		})
	}
}
