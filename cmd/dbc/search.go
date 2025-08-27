// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/list"
	"github.com/charmbracelet/lipgloss/tree"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
)

var (
	nameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("35"))
	descStyle = lipgloss.NewStyle().Italic(true)
	bold      = lipgloss.NewStyle().Bold(true)

	archStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
)

type SearchCmd struct {
	Verbose   bool           `arg:"-v" help:"Enable verbose output"`
	Pattern   *regexp.Regexp `arg:"positional" help:"Pattern to search for"`
	NamesOnly bool           `arg:"-n" help:"Restrict search to names, ignoring descriptions"`
}

func (s SearchCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return searchModel{
		verbose:   s.Verbose,
		pattern:   s.Pattern,
		namesOnly: s.NamesOnly,
		baseModel: baseModel,
	}
}

func (s SearchCmd) GetModel() tea.Model {
	return searchModel{
		verbose:   s.Verbose,
		pattern:   s.Pattern,
		namesOnly: s.NamesOnly,
		baseModel: baseModel{
			getDriverList: getDriverList,
			downloadPkg:   downloadPkg,
		},
	}
}

type searchModel struct {
	baseModel

	verbose   bool
	pattern   *regexp.Regexp
	namesOnly bool
}

func (m searchModel) Init() tea.Cmd {
	return func() tea.Msg {
		drivers, err := m.getDriverList()
		if err != nil {
			return err
		}

		return m.filterDrivers(drivers)
	}
}

func (m searchModel) filterDrivers(drivers []dbc.Driver) []dbc.Driver {
	if m.pattern == nil {
		return drivers
	}

	var results []dbc.Driver
	for _, d := range drivers {
		if m.pattern.MatchString(d.Path) || m.pattern.MatchString(d.Title) || (!m.namesOnly && m.pattern.MatchString(d.Desc)) {
			results = append(results, d)
		}
	}
	return results
}

func (m searchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case []dbc.Driver:
		return m, tea.Sequence(
			tea.Println(viewDrivers(msg, m.verbose)), tea.Quit)
	default:
		bm, cmd := m.baseModel.Update(msg)
		m.baseModel = bm.(baseModel)

		return m, cmd
	}
}

func (m searchModel) View() string { return "" }

func emptyEnumerator(_ list.Items, _ int) string {
	return ""
}

func viewDrivers(d []dbc.Driver, verbose bool) string {
	current := config.Get()
	installedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	l := list.New()
	for _, driver := range d {
		var installed []string
		installedVerbose := make(map[string][]string)

		for k, v := range current {
			if drv, ok := v.Drivers[driver.Path]; ok {
				installed = append(installed, fmt.Sprintf("%s=>%s", k, drv.Version))
				existing := installedVerbose[drv.Version.String()]
				installedVerbose[drv.Version.String()] = append(existing, fmt.Sprintf("%s => %s", k, drv.FilePath))
			}
		}

		var suffix string
		if len(installed) > 0 {
			suffix = installedStyle.Render(" [installed: " + strings.Join(installed, ", ") + "]")
		}

		if !verbose {
			l.Item(nameStyle.Render(driver.Path) + " - " + descStyle.Render(driver.Desc) + suffix)
			continue
		}

		var installedVersionTree any
		if len(installedVerbose) > 0 {
			vtree := tree.Root(bold.Render("Installed Versions:")).
				Enumerator(tree.RoundedEnumerator)
			for k, v := range installedVerbose {
				child := tree.Root(k)
				for _, loc := range v {
					child.Child(loc)
				}
				vtree.Child(child)
			}
			installedVersionTree = vtree
		}

		versionTree := tree.Root(bold.Render("Available Versions:")).
			Enumerator(tree.RoundedEnumerator)
		for _, v := range driver.Versions(config.PlatformTuple()) {
			versionTree.Child(v)
		}

		l.Item(nameStyle.Render(driver.Path) + suffix).Item(
			list.New(bold.Render("Title: ")+descStyle.Render(driver.Title), bold.Render("Description: ")+descStyle.Render(driver.Desc),
				bold.Render("License: ")+driver.License,
				installedVersionTree,
				versionTree,
			).Enumerator(emptyEnumerator))
	}

	return l.String()
}
