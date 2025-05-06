// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/tree"
	"github.com/columnar-tech/dbc/config"
)

type ViewConfigCmd struct{}

func (ViewConfigCmd) GetModel() tea.Model {
	return simpleViewConfigModel{}
}

type simpleViewConfigModel struct{}

func (m simpleViewConfigModel) Init() tea.Cmd {
	return func() tea.Msg {
		return config.Get()
	}
}

func (m simpleViewConfigModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case map[config.ConfigLevel]config.Config:
		return m, tea.Sequence(
			tea.Println(viewConfig(msg[config.ConfigEnv])),
			tea.Println(viewConfig(msg[config.ConfigUser])),
			tea.Println(viewConfig(msg[config.ConfigSystem])),
			tea.Quit)
	}

	return m, nil
}

func (m simpleViewConfigModel) View() string { return "" }

func viewConfig(cfg config.Config) string {
	if cfg.Level == config.ConfigUnknown {
		return ""
	}

	t := tree.Root(cfg.Level.String() + ": " + cfg.Location).
		RootStyle(nameStyle)
	if !cfg.Exists {
		t.Child(descStyle.Bold(true).Foreground(lipgloss.Color("1")).Render("does not exist"))
	} else {
		for _, d := range cfg.Drivers {
			t.Child(tree.New().Root(d.Name).
				Child(descStyle.Render(d.ID) + " (" + d.Version + ")"))
		}
	}

	return t.String() + "\n"
}
