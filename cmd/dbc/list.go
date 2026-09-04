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
	"os"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

type ListCmd struct {
	Level        config.ConfigLevel `arg:"-l" help:"Only list drivers installed at this config level (user, system)"`
	AllPlatforms bool               `arg:"--all-platforms" help:"List drivers for all platforms, including those not loadable on this host"`
	Json         bool               `arg:"--json" help:"Print output as JSON instead of plaintext"`
}

func (ListCmd) Description() string {
	return "List installed drivers across user, system, and environment config levels."
}

func (c ListCmd) GetModel() tea.Model {
	return listModel{
		level:        c.Level,
		allPlatforms: c.AllPlatforms,
		jsonOutput:   c.Json,
	}
}

type installedDriver struct {
	Level    config.ConfigLevel
	ID       string
	Name     string
	Version  string
	Path     string
	Platform string
}

type installedDriversMsg []installedDriver

type listModel struct {
	baseModel

	level        config.ConfigLevel
	allPlatforms bool
	jsonOutput   bool
	drivers      []installedDriver
}

func driverAvailableOnHost(d config.DriverInfo, hostPlatform string) bool {
	return d.Driver.Shared.HasPlatform(hostPlatform)
}

func formatPlatformsColumn(d config.DriverInfo, hostPlatform string) string {
    // If the driver is configured with a default path (i.e. no explicit 
	// `<platform> = '/path/to/driver.so'` in the manifest),
	// then we assume that the driver is a match for the host platform.
	if d.Driver.Shared.UsesDefaultPath() {
		return hostPlatform + " (*)"
	}

	platforms := d.Driver.Shared.PlatformTuples()
	if len(platforms) == 0 {
		return ""
	}

	parts := make([]string, len(platforms))
	for i, platform := range platforms {
		if platform == hostPlatform {
			parts[i] = platform + " (*)"
		} else {
			parts[i] = platform
		}
	}
	return strings.Join(parts, ", ")
}

func (m listModel) Init() tea.Cmd {
	return func() tea.Msg {
		cfgs := config.Get()
		hostPlatform := config.PlatformTuple()

		var levels []config.ConfigLevel
		if m.level == config.ConfigUnknown {
			levels = []config.ConfigLevel{config.ConfigSystem, config.ConfigUser, config.ConfigEnv}
		} else {
			levels = []config.ConfigLevel{m.level}
		}

		var drivers []installedDriver
		for _, lvl := range levels {
			cfg, ok := cfgs[lvl]
			if !ok {
				continue
			}
			if cfg.Err != nil {
				return fmt.Errorf("failed to list drivers at %s level: %w", lvl, cfg.Err)
			}
			for _, d := range cfg.Drivers {
				if !m.allPlatforms && !driverAvailableOnHost(d, hostPlatform) {
					continue
				}

				version := ""
				if d.Version != nil {
					version = d.Version.String()
				}

				entry := installedDriver{
					Level:   lvl,
					ID:      d.ID,
					Name:    d.Name,
					Version: version,
					Path:    d.FilePath,
				}
				if m.allPlatforms {
					entry.Platform = formatPlatformsColumn(d, hostPlatform)
				}
				drivers = append(drivers, entry)
			}
		}

		sort.Slice(drivers, func(i, j int) bool {
			if drivers[i].Level != drivers[j].Level {
				return drivers[i].Level > drivers[j].Level
			}
			return drivers[i].ID < drivers[j].ID
		})

		return installedDriversMsg(drivers)
	}
}

func (m listModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case installedDriversMsg:
		m.drivers = []installedDriver(msg)
		return m, tea.Quit
	default:
		bm, cmd := m.baseModel.Update(msg)
		m.baseModel = bm.(baseModel)
		return m, cmd
	}
}

func (m listModel) View() tea.View { return tea.NewView("") }

func (m listModel) IsJSONMode() bool { return m.jsonOutput }

func (m listModel) FinalOutput() string {
	if m.status != 0 {
		if m.jsonOutput {
			return marshalEnvelope("error", jsonschema.ErrorResponse{
				Code:    "list_failed",
				Message: m.err.Error(),
			})
		}
		return ""
	}

	if m.jsonOutput {
		return listDriversJSON(m.drivers, m.allPlatforms)
	}
	return formatInstalledDrivers(m.drivers, m.allPlatforms)
}

func formatInstalledDrivers(drivers []installedDriver, allPlatforms bool) string {
	if len(drivers) == 0 {
		lipgloss.Fprintln(os.Stderr, "No drivers installed.")
		return ""
	}

	headers := []string{"DRIVER", "VERSION", "LEVEL", "LOCATION"}
	if allPlatforms {
		headers = []string{"DRIVER", "VERSION", "PLATFORM", "LEVEL", "LOCATION"}
	}

	t := table.New().Border(lipgloss.HiddenBorder()).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		Headers(headers...)
	headerStyle := lipgloss.NewStyle().Bold(true)
	levelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	versionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return headerStyle
		}
		switch col {
		case 0:
			return nameStyle
		case 1:
			return versionStyle
		case 2:
			if allPlatforms {
				return lipgloss.NewStyle()
			}
			return levelStyle
		case 3:
			if allPlatforms {
				return levelStyle
			}
		}
		return lipgloss.NewStyle()
	})
	for _, d := range drivers {
		if allPlatforms {
			t.Row(d.ID, d.Version, d.Platform, d.Level.String(), d.Path)
		} else {
			t.Row(d.ID, d.Version, d.Level.String(), d.Path)
		}
	}

	return strings.TrimRight(t.String(), "\n")
}

func listDriversJSON(drivers []installedDriver, allPlatforms bool) string {
	entries := make([]jsonschema.ListDriverEntry, 0, len(drivers))
	for _, d := range drivers {
		entry := jsonschema.ListDriverEntry{
			Driver:   d.ID,
			Name:     d.Name,
			Version:  d.Version,
			Level:    d.Level.String(),
			Location: d.Path,
		}
		if allPlatforms {
			entry.Platform = d.Platform
		}
		entries = append(entries, entry)
	}
	payloadBytes, err := json.Marshal(jsonschema.ListResponse{Drivers: entries})
	if err != nil {
		return fmt.Sprintf("error marshaling JSON: %v", err)
	}
	env := jsonschema.Envelope{
		SchemaVersion: jsonschema.SchemaVersion,
		Kind:          "list.results",
		Payload:       json.RawMessage(payloadBytes),
	}
	out, err := json.Marshal(env)
	if err != nil {
		return fmt.Sprintf("error marshaling JSON: %v", err)
	}
	return string(out)
}
