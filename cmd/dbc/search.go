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
)

type SearchCmd struct {
	Verbose bool           `arg:"-v" help:"Enable verbose output"`
	Pattern *regexp.Regexp `arg:"positional" help:"Pattern to search for"`
}

func (s SearchCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return searchModel{
		verbose:   s.Verbose,
		pattern:   s.Pattern,
		baseModel: baseModel,
	}
}

func (s SearchCmd) GetModel() tea.Model {
	return searchModel{
		verbose: s.Verbose,
		pattern: s.Pattern,
		baseModel: baseModel{
			getDriverRegistry: getDriverRegistry,
			downloadPkg:       downloadPkg,
		},
	}
}

type searchModel struct {
	baseModel

	verbose      bool
	pattern      *regexp.Regexp
	finalDrivers []dbc.Driver
}

func (m searchModel) Init() tea.Cmd {
	return func() tea.Msg {
		drivers, err := m.getDriverRegistry()
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
		if m.pattern.MatchString(d.Path) || m.pattern.MatchString(d.Title) || m.pattern.MatchString(d.Desc) {
			results = append(results, d)
		}
	}
	return results
}

func (m searchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case []dbc.Driver:
		m.finalDrivers = msg
		return m, tea.Sequence(tea.Quit)
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

		l.Item(nameStyle.Render(driver.Path)).Item(
			list.New(bold.Render("Title: ")+descStyle.Render(driver.Title), bold.Render("Description: ")+descStyle.Render(driver.Desc),
				bold.Render("License: ")+driver.License,
				installedVersionTree,
				versionTree,
			).Enumerator(emptyEnumerator))
	}

	return l.String() + "\n"
}

func (m searchModel) FinalOutput() string {
	return viewDrivers(m.finalDrivers, m.verbose)
}
