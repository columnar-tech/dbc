// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc/config"
)

func getTuiModel() tea.Model {
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

	return m
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
