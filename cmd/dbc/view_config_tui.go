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
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/columnar-tech/dbc/config"
)

var (
	titleStyle      = lipgloss.NewStyle().MarginLeft(2)
	paginationStyle = list.DefaultStyles(true).PaginationStyle.PaddingLeft(4)
	helpStyle       = list.DefaultStyles(true).HelpStyle.PaddingLeft(4).PaddingBottom(1)

	modelStyle = lipgloss.NewStyle().
			Width(60).
			Height(5).
			Align(lipgloss.Top, lipgloss.Left).
			BorderStyle(lipgloss.HiddenBorder())
)

type configViewModel struct {
	Prev tea.Model

	Drivers []config.DriverInfo
	list    list.Model
}

var (
	configStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170"))
)

type driverItem config.DriverInfo

func (d driverItem) FilterValue() string { return d.ID }
func (d driverItem) String() string {
	return d.Name + " (" + d.Version.String() + ")"
}

func (d driverItem) View() string {
	var sb strings.Builder
	sb.WriteString(d.Name + "(" + d.Version.String() + ")\n")
	sb.WriteString("Publisher: " + d.Publisher + "\n")
	sb.WriteString("License: " + d.License + "\n")
	sb.WriteString("Source: " + d.Source + "\n")
	sb.WriteString("\n\n")
	sb.WriteString("Driver Location: \n")
	sb.WriteString(d.Driver.Shared.String() + "\n")
	return configStyle.Render(sb.String())
}

func toListItems(drivers []config.DriverInfo) []list.Item {
	items := make([]list.Item, len(drivers))
	for i, d := range drivers {
		items[i] = driverItem(d)
	}
	return items
}

func (m configViewModel) Init() tea.Cmd {
	return func() tea.Msg {
		return append(config.FindDriverConfigs(config.ConfigUser),
			config.FindDriverConfigs(config.ConfigSystem)...)
	}
}

func (m configViewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case []config.DriverInfo:
		m.list = list.New(toListItems(msg), SimpleItemDelegate{Prompt: ">"}, 20, 14)
		m.list.Title = "Available Drivers"
		m.list.SetShowStatusBar(false)
		m.list.SetFilteringEnabled(false)
		m.list.Styles.Title = titleStyle
		m.list.Styles.PaginationStyle = paginationStyle
		m.list.Styles.HelpStyle = helpStyle
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q", "esc":
			return m.Prev, nil
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m configViewModel) View() tea.View {
	var sb strings.Builder
	sb.WriteString("DBC Driver Config\n\n")
	// sb.WriteString(configStyle.Render("System Driver Directory:      "+systemDriversDir,
	// 	"\nEnvironment Driver Directory: "+envDriversDir+"\n"))

	sb.WriteString("\n")

	var bottomView string
	if m.list.SelectedItem() != nil {
		bottomView = m.list.SelectedItem().(driverItem).View()
	}

	return tea.NewView(lipgloss.JoinVertical(lipgloss.Top, sb.String()+m.list.View(),
		modelStyle.Render(bottomView)))
}
