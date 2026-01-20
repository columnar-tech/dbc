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
			// tea.Println(viewConfig(msg[config.ConfigEnv])),
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
				Child(descStyle.Render(d.ID) + " (" + d.Version.String() + ")"))
		}
	}

	return t.String() + "\n"
}
