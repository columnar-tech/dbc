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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Masterminds/semver/v3"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc/config"
	"github.com/pelletier/go-toml/v2"
)

var msgStyle = lipgloss.NewStyle().Faint(true)

func driverListPath(path string) (string, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	if filepath.Ext(p) == "" {
		p = filepath.Join(p, "dbc.toml")
	}
	return p, nil
}

type AddCmd struct {
	Driver []string `arg:"positional,required" help:"Driver to add"`
	Path   string   `arg:"-p" placeholder:"FILE" default:"./dbc.toml" help:"Driver list to add to"`
	Pre    bool     `arg:"--pre" help:"Allow pre-release versions implicitly"`
}

func (c AddCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return addModel{
		baseModel: baseModel,
		Driver:    c.Driver,
		Path:      c.Path,
		Pre:       c.Pre,
	}
}

func (c AddCmd) GetModel() tea.Model {
	return addModel{
		Driver: c.Driver,
		Path:   c.Path,
		Pre:    c.Pre,
		baseModel: baseModel{
			getDriverRegistry: getDriverRegistry,
			downloadPkg:       downloadPkg,
		},
	}
}

type addModel struct {
	baseModel

	Driver []string
	Path   string
	Pre    bool
	list   DriversList
}

func (m addModel) Init() tea.Cmd {
	type driverInput struct {
		Name string
		Vers *semver.Constraints
	}

	var specs []driverInput
	for _, d := range m.Driver {
		driverName, vers, err := parseDriverConstraint(d)
		if err != nil {
			return errCmd("invalid driver constraint '%s': %w", d, err)
		}

		specs = append(specs, driverInput{Name: driverName, Vers: vers})
	}

	return func() tea.Msg {
		drivers, registryErr := m.getDriverRegistry()
		// If we have no drivers and there's an error, fail immediately
		if len(drivers) == 0 && registryErr != nil {
			return fmt.Errorf("error getting driver list: %w", registryErr)
		}
		// Store registry errors to use later if driver is not found
		// We continue processing if we have some drivers
		var registryErrors error = registryErr

		p, err := driverListPath(m.Path)
		if err != nil {
			return err
		}

		f, err := os.Open(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("error opening driver list: %s doesn't exist\nDid you run `dbc init`?", m.Path)
			} else {
				return fmt.Errorf("error opening driver list at %s: %w", m.Path, err)
			}
		}
		defer f.Close()

		if err := toml.NewDecoder(f).Decode(&m.list); err != nil {
			return err
		}

		if m.list.Drivers == nil {
			m.list.Drivers = make(map[string]driverSpec)
		}

		var result string
		for i, spec := range specs {
			if i != 0 {
				result += "\n"
			}

			drv, err := findDriver(spec.Name, drivers)
			if err != nil {
				// If we have registry errors, enhance the error message
				if registryErrors != nil {
					return fmt.Errorf("%w\n\nNote: Some driver registries were unavailable:\n%s", err, registryErrors.Error())
				}
				return err
			}

			if spec.Vers != nil {
				spec.Vers.IncludePrerelease = m.Pre
				_, err = drv.GetWithConstraint(spec.Vers, config.PlatformTuple())
				if err != nil {
					return fmt.Errorf("error getting driver: %w", err)
				}
			} else {
				if !m.Pre && !drv.HasNonPrerelease() {
					err := fmt.Errorf("driver `%s` not found in driver registry index", spec.Name)
					// If we have registry errors, enhance the error message
					if registryErrors != nil {
						return fmt.Errorf("%w\n\nNote: Some driver registries were unavailable:\n%s", err, registryErrors.Error())
					}
					return err
				}
			}

			current, ok := m.list.Drivers[spec.Name]
			m.list.Drivers[spec.Name] = driverSpec{Version: spec.Vers}
			if m.Pre {
				m.list.Drivers[spec.Name] = driverSpec{Version: spec.Vers, Prerelease: "allow"}
			}

			new := m.list.Drivers[spec.Name]
			currentString := func() string {
				if current.Version != nil {
					return current.Version.String()
				}
				return "any"
			}()
			newStr := func() string {
				if new.Version != nil {
					return new.Version.String()
				}
				return "any"
			}()
			if ok {
				result = msgStyle.Render(fmt.Sprintf("replacing existing driver %s (old constraint: %s; new constraint: %s)",
					spec.Name, currentString, newStr)) + "\n"
			}

			result += nameStyle.Render("added", spec.Name, "to driver list")
			if spec.Vers != nil {
				result += nameStyle.Render(" with constraint", spec.Vers.String())
			}
		}

		f, err = os.Create(p)
		if err != nil {
			return fmt.Errorf("error creating file %s: %w", p, err)
		}
		defer f.Close()

		if err := toml.NewEncoder(f).Encode(m.list); err != nil {
			return err
		}
		result += "\nuse `dbc sync` to install the drivers in the list"
		return result
	}
}

func (m addModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case string:
		return m, tea.Sequence(tea.Println(msg), tea.Quit)
	default:
		bm, cmd := m.baseModel.Update(msg)
		m.baseModel = bm.(baseModel)

		return m, cmd
	}
}

func (m addModel) View() string { return "" }
