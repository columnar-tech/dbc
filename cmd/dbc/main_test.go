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
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

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
			go func() { m, err = p.Run() }()

			<-time.After(time.Second * 1)

			p.Wait()
			require.NoError(t, err, out.String())

			if h, ok := m.(HasStatus); ok {
				require.Equal(t, tt.status, h.Status(), "name=%q: cmd=%#v", tt.name, tt.cmd)
			} else {
				t.Fatalf("model doesn't implement HasStatus")
			}
		})
	}
}
