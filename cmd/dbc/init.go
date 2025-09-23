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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

type InitCmd struct {
	Path string `arg:"positional" default:"./dbc.toml" help:"File to create"`
}

func (c InitCmd) GetModel() tea.Model {
	return initModel{Path: c.Path}
}

type initModel struct {
	Path string

	status int
}

const initialList = `# dbc driver list

[drivers]
`

func (m initModel) Status() int {
	return m.status
}

func (m initModel) Init() tea.Cmd {
	return func() tea.Msg {
		p, err := driverListPath(m.Path)
		if err != nil {
			return err
		}

		_, err = os.Stat(p)
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("file %s already exists", p)
		}

		if err = os.MkdirAll(filepath.Dir(p), 0777); err != nil {
			return fmt.Errorf("error creating directory for %s: %w", p, err)
		}

		if err := os.WriteFile(p, []byte(initialList), 0666); err != nil {
			return fmt.Errorf("error creating file %s: %w", p, err)
		}

		return tea.Quit()
	}
}

func (m initModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case error:
		m.status = 1
		return m, tea.Sequence(
			tea.Println("Error: ", msg.Error()), tea.Quit)
	}
	return m, nil
}

func (m initModel) View() string { return "" }
