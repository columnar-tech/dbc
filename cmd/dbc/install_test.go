// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type seqTest struct {
	seq []byte
	msg tea.Msg
}

func TestCmd(t *testing.T) {

	tests := []struct {
		name      string
		cmd       modelCmd
		input     []seqTest
		postCheck func(t *testing.T, tmpdir string)
	}{
		{"install bigquery",
			InstallCmd{Driver: "bigquery", Level: config.ConfigEnv},
			[]seqTest{
				{seq: []byte("y"), msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}},
				{seq: []byte("\r"), msg: tea.KeyMsg{Type: tea.KeyEnter}},
			},
			func(t *testing.T, tmpdir string) {
				if runtime.GOOS != "windows" {
					assert.FileExists(t, filepath.Join(tmpdir, "bigquery.toml"))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			require.NoError(t, os.Setenv("ADBC_DRIVERS_DIR", tmpdir))
			defer os.Unsetenv("ADBC_DRIVERS_DIR")

			var in bytes.Buffer
			var out bytes.Buffer

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			p := tea.NewProgram(tt.cmd.GetModel(), tea.WithInput(&in),
				tea.WithOutput(&out), tea.WithContext(ctx))

			var err error
			go func() { _, err = p.Run() }()

			for _, s := range tt.input {
				<-time.After(time.Millisecond * 500)
				require.NoError(t, ctx.Err())
				in.Write(s.seq)
				p.Send(s.msg)
			}

			p.Wait()
			require.NoError(t, err, out.String())
			tt.postCheck(t, tmpdir)
		})
	}
}
