// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"
	"os"

	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
)

const defaultWidth = 40

var (
	docStyle          = lipgloss.NewStyle().Margin(1, 2)
	itemStyle         = lipgloss.NewStyle().PaddingLeft(4)
	selectedItemStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170"))
)

type item struct {
	d dbc.Driver
}

func (i item) Title() string       { return i.d.Title }
func (i item) Description() string { return i.d.Desc }
func (i item) FilterValue() string { return i.d.Title }

type model struct {
	Prev tea.Model

	list          list.Model
	chooseVersion versionModel
	quitting      bool
}

func getDrivers() tea.Msg {
	drivers, err := dbc.GetDriverList()
	if err != nil {
		fmt.Println("Error getting drivers:", err)
		os.Exit(1)
	}

	items := []list.Item{}
	for _, d := range drivers {
		items = append(items, item{d: d})
	}

	return items
}

func (m model) Init() tea.Cmd {
	return getDrivers
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case []list.Item:
		m.list.SetItems(msg)
	case tea.KeyMsg:
		switch keypress := msg.String(); keypress {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "q", "esc":
			return m.Prev, nil
		case "enter":
			i, ok := m.list.SelectedItem().(item)
			if ok {
				versions := []list.Item{}
				for _, v := range i.d.Versions(config.PlatformTuple()) {
					versions = append(versions, versionOption(*v))
				}

				m.chooseVersion = versionModel{
					list:   list.New(versions, dbc.SimpleItemDelegate{Prompt: ">"}, 40, 15),
					choice: "",
				}
				m.chooseVersion.list.Title = fmt.Sprintf("Versions for %s", i.d.Title)
				m.chooseVersion.list.SetShowStatusBar(false)
				m.chooseVersion.list.SetFilteringEnabled(false)
				m.chooseVersion.list.SetShowHelp(false)
			}
		}

	case tea.WindowSizeMsg:
		h, _ := docStyle.GetFrameSize()
		m.list.SetWidth(msg.Width - h)
	}

	var cmd tea.Cmd
	if len(m.chooseVersion.list.Items()) != 0 {
		m.chooseVersion.list, cmd = m.chooseVersion.list.Update(msg)
	} else {
		m.list, cmd = m.list.Update(msg)
	}
	return m, cmd
}

func (m model) View() string {
	if len(m.chooseVersion.list.Items()) != 0 {
		return "\n" + m.chooseVersion.list.View()
	}
	return "\n" + m.list.View()
}

type versionOption semver.Version

func (v versionOption) FilterValue() string { return v.String() }
func (v versionOption) String() string      { return v.String() }

type versionModel struct {
	list   list.Model
	choice string
}
