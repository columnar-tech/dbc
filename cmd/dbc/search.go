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
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/list"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/lipgloss/tree"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
)

var (
	nameStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("35"))
	descStyle     = lipgloss.NewStyle().Italic(true)
	bold          = lipgloss.NewStyle().Bold(true)
	registryStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
)

type SearchCmd struct {
	Verbose bool           `arg:"-v" help:"Enable verbose output"`
	Json    bool           `help:"Print output as JSON instead of plaintext"`
	Pattern *regexp.Regexp `arg:"positional" help:"Pattern to search for"`
	Pre     bool           `arg:"--pre" help:"Include pre-release drivers and versions (hidden by default)"`
}

func (s SearchCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return searchModel{
		verbose:    s.Verbose,
		outputJson: s.Json,
		pattern:    s.Pattern,
		pre:        s.Pre,
		baseModel:  baseModel,
	}
}

func (s SearchCmd) GetModel() tea.Model {
	return s.GetModelCustom(baseModel{
		getDriverRegistry: getDriverRegistry,
		downloadPkg:       downloadPkg,
	})
}

type searchModel struct {
	baseModel

	verbose        bool
	outputJson     bool
	pre            bool
	pattern        *regexp.Regexp
	finalDrivers   []dbc.Driver
	registryErrors error // Store registry errors to display as warnings
}

type driversWithErrorMsg struct {
	drivers []dbc.Driver
	err     error
}

func (m searchModel) Init() tea.Cmd {
	return func() tea.Msg {
		drivers, err := m.getDriverRegistry()
		// Don't fail completely if we have some drivers - return them with the error
		// This allows graceful degradation when some registries fail
		return driversWithErrorMsg{
			drivers: m.filterDrivers(drivers),
			err:     err,
		}
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
	case driversWithErrorMsg:
		m.finalDrivers = msg.drivers
		m.registryErrors = msg.err
		// If we have no drivers and there's an error, fail the command
		if len(msg.drivers) == 0 && msg.err != nil {
			m.err = msg.err
			m.status = 1
		}
		return m, tea.Sequence(tea.Quit)
	case []dbc.Driver:
		// For backwards compatibility, still handle plain driver list
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

func viewDrivers(d []dbc.Driver, verbose bool, allowPre bool) string {
	if len(d) == 0 {
		return ""
	}

	current := config.Get()
	installedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	l := list.New()
	t := table.New().Border(lipgloss.HiddenBorder()).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false)
	for _, driver := range d {
		installed, installedVerbose := getInstalled(driver, current)

		var suffix string
		if len(installed) > 0 {
			suffix = installedStyle.Render(" [installed: " + strings.Join(installed, ", ") + "]")
		} else if !allowPre && !driver.HasNonPrerelease() {
			continue
		}

		var regTag string
		if driver.Registry.Name != "" {
			regTag = registryStyle.Render(" [" + driver.Registry.Name + "]")
		}

		if !verbose {
			t.Row(nameStyle.Render(driver.Path)+regTag,
				descStyle.Render(driver.Desc), suffix)
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
			if v.Prerelease() != "" && !allowPre {
				continue
			}

			versionTree.Child(v)
		}

		l.Item(nameStyle.Render(driver.Path) + regTag).Item(
			list.New(bold.Render("Title: ")+descStyle.Render(driver.Title), bold.Render("Description: ")+descStyle.Render(driver.Desc),
				bold.Render("License: ")+driver.License,
				installedVersionTree,
				versionTree,
			).Enumerator(emptyEnumerator))
	}

	if !verbose {
		return t.String()
	}
	return l.String()
}

func viewDriversJSON(d []dbc.Driver, verbose bool, allowPre bool) string {
	current := config.Get()

	if !verbose {
		type output struct {
			Driver      string   `json:"driver"`
			Description string   `json:"description"`
			Installed   []string `json:"installed,omitempty"`
			Registry    string   `json:"registry,omitempty"`
		}

		var result []output
		for _, driver := range d {
			installed, _ := getInstalled(driver, current)
			if !allowPre && !driver.HasNonPrerelease() && len(installed) == 0 {
				continue
			}

			result = append(result, output{
				Driver:      driver.Path,
				Description: driver.Desc,
				Installed:   installed,
				Registry:    driver.Registry.Name,
			})
		}

		jsonBytes, err := json.Marshal(result)
		if err != nil {
			return fmt.Sprintf("error marshaling JSON: %v", err)
		}
		return string(jsonBytes)
	}

	type output struct {
		Driver            string              `json:"driver"`
		Description       string              `json:"description"`
		License           string              `json:"license"`
		Registry          string              `json:"registry,omitempty"`
		InstalledVersions map[string][]string `json:"installed_versions,omitempty"`
		AvailableVersions []string            `json:"available_versions,omitempty"`
	}

	var result []output
	for _, driver := range d {
		_, installedVerbose := getInstalled(driver, current)

		var availableVersions []string
		for _, v := range driver.Versions(config.PlatformTuple()) {
			if v.Prerelease() != "" && !allowPre {
				continue
			}

			availableVersions = append(availableVersions, v.String())
		}

		result = append(result, output{
			Driver:            driver.Path,
			Description:       driver.Desc,
			License:           driver.License,
			Registry:          driver.Registry.Name,
			InstalledVersions: installedVerbose,
			AvailableVersions: availableVersions,
		})
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf("error marshaling JSON: %v", err)
	}
	return string(jsonBytes)
}

func getInstalled(driver dbc.Driver, cfg map[config.ConfigLevel]config.Config) ([]string, map[string][]string) {
	var installed []string
	installedVerbose := make(map[string][]string)

	for k, v := range cfg {
		if drv, ok := v.Drivers[driver.Path]; ok {
			installed = append(installed, fmt.Sprintf("%s=>%s", k, drv.Version))
			existing := installedVerbose[drv.Version.String()]
			installedVerbose[drv.Version.String()] = append(existing, fmt.Sprintf("%s => %s", k, drv.FilePath))
		}
	}
	return installed, installedVerbose
}

func (m searchModel) FinalOutput() string {
	var output string

	// Display warning about registry errors if any occurred
	if m.registryErrors != nil {
		warningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
		output = warningStyle.Render("Warning: ") + "Some driver registries were unavailable:\n"
		output += m.registryErrors.Error() + "\n\n"
	}

	if m.outputJson {
		output += viewDriversJSON(m.finalDrivers, m.verbose, m.pre)
	} else {
		output += viewDrivers(m.finalDrivers, m.verbose, m.pre)
	}

	return output
}
