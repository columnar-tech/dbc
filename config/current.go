// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

import (
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc"
)

var (
	titleStyle      = lipgloss.NewStyle().MarginLeft(2)
	paginationStyle = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	helpStyle       = list.DefaultStyles().HelpStyle.PaddingLeft(4).PaddingBottom(1)

	modelStyle = lipgloss.NewStyle().
			Width(60).
			Height(5).
			Align(lipgloss.Top, lipgloss.Left).
			BorderStyle(lipgloss.HiddenBorder())
)

type Model struct {
	Prev tea.Model

	Drivers []DriverInfo
	list    list.Model
}

const systemDriversDir = "/etc/adbc_drivers"

var (
	envDriversDir = os.Getenv("ADBC_DRIVERS_DIR")
	configStyle   = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170"))
)

type driverItem DriverInfo

func (d driverItem) FilterValue() string { return d.ID }
func (d driverItem) String() string {
	return d.Name + " (" + d.Version + ")"
}

func (d driverItem) View() string {
	var sb strings.Builder
	sb.WriteString(d.Name + "(" + d.Version + ")\n")
	sb.WriteString("Publisher: " + d.Publisher + "\n")
	sb.WriteString("License: " + d.License + "\n")
	sb.WriteString("Source: " + d.Source + "\n")
	sb.WriteString("\n\n")
	sb.WriteString("Driver Location: " + d.Driver.Shared + "\n")
	return sb.String()
}

func toListItems(drivers []DriverInfo) []list.Item {
	items := make([]list.Item, len(drivers))
	for i, d := range drivers {
		items[i] = driverItem(d)
	}
	return items
}

func (m Model) Init() tea.Cmd {
	return func() tea.Msg {
		return append(FindDriverConfigs(envDriversDir),
			FindDriverConfigs(systemDriversDir)...)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case []DriverInfo:
		m.list = list.New(toListItems(msg), dbc.SimpleItemDelegate{Prompt: ">"}, 20, 14)
		m.list.Title = "Available Drivers"
		m.list.SetShowStatusBar(false)
		m.list.SetFilteringEnabled(false)
		m.list.Styles.Title = titleStyle
		m.list.Styles.PaginationStyle = paginationStyle
		m.list.Styles.HelpStyle = helpStyle
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		return m, nil
	case tea.KeyMsg:
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

func (m Model) View() string {
	var sb strings.Builder
	sb.WriteString("DBC Driver Config\n\n")
	sb.WriteString(configStyle.Render("System Driver Directory:      "+systemDriversDir,
		"\nEnvironment Driver Directory: "+envDriversDir+"\n"))

	sb.WriteString("\n")

	var bottomView string
	if m.list.SelectedItem() != nil {
		bottomView = m.list.SelectedItem().(driverItem).View()
	}

	return lipgloss.JoinVertical(lipgloss.Top, sb.String()+m.list.View(),
		modelStyle.Render(bottomView))
}
