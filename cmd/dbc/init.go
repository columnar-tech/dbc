// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

type InitCmd struct {
	Path string `arg:"-p" placeholder:"PATH" default:"./dbc.toml" help:"File to create"`
}

func (f InitCmd) GetModel() tea.Model {
	return initModel{Path: f.Path}
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
		p, err := filepath.Abs(m.Path)
		if err != nil {
			return fmt.Errorf("invalid path: %w", err)
		}

		info, err := os.Stat(p)
		if err == nil && info.IsDir() {
			p = filepath.Join(p, "dbc.toml")
		}

		if err := os.WriteFile(p, []byte(initialList), 0644); err != nil {
			return fmt.Errorf("error creating file %s: %w", p, err)
		}

		return tea.Quit()
	}
}

func (m initModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case error:
		m.status = 1
		tea.Println("Error:", msg.Error())
		return m, tea.Quit
	}
	return m, tea.Quit
}

func (m initModel) View() string { return "" }
