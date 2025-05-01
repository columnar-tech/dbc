// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"strings"

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
				for _, v := range i.d.Versions {
					versions = append(versions, versionOption(v))
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

type menu struct {
	options list.Model
}

func (m menu) Init() tea.Cmd { return nil }

func (m menu) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch keypress := msg.String(); keypress {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "enter":
			i, ok := m.options.SelectedItem().(menuOption)
			if ok {
				return i.delegate, i.delegate.Init()
			}
		}
	case tea.WindowSizeMsg:
		m.options.SetWidth(msg.Width)
	}

	var cmd tea.Cmd
	m.options, cmd = m.options.Update(msg)
	return m, cmd
}

func (m menu) View() string {
	return "\n" + m.options.View()
}

type menuOption struct {
	title    string
	delegate tea.Model
}

func (m menuOption) FilterValue() string { return m.title }

type menuDelegate struct{}

func (d menuDelegate) Height() int                             { return 1 }
func (d menuDelegate) Spacing() int                            { return 0 }
func (d menuDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d menuDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(menuOption)
	if !ok {
		return
	}

	str := i.title
	fn := itemStyle.Render
	if index == m.Index() {
		fn = func(s ...string) string {
			return selectedItemStyle.Render("> " + strings.Join(s, " "))
		}
	}

	fmt.Fprint(w, fn(str))
}

func main() {
	m := menu{options: list.New([]list.Item{}, menuDelegate{}, defaultWidth, 12)}
	m.options.Title = "Menu Options"
	m.options.SetShowStatusBar(false)
	m.options.SetFilteringEnabled(false)
	m.options.SetShowHelp(true)

	driversModel := model{
		Prev: &m,
		list: list.New([]list.Item{}, list.NewDefaultDelegate(),
			defaultWidth, 15)}
	driversModel.list.Title = "Drivers"
	driversModel.list.SetFilteringEnabled(true)

	m.options.SetItems([]list.Item{
		menuOption{title: "Current Config", delegate: config.Model{Prev: &m}},
		menuOption{title: "Drivers", delegate: &driversModel},
	})

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}

type versionOption string

func (v versionOption) FilterValue() string { return string(v) }
func (v versionOption) String() string      { return string(v) }

type versionModel struct {
	list   list.Model
	choice string
}
