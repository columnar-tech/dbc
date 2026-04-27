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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/pelletier/go-toml/v2"
)

var msgStyle = lipgloss.NewStyle().Faint(true)

func marshalEnvelope(kind string, payload any) string {
	payloadBytes, _ := json.Marshal(payload)
	env := jsonschema.Envelope{
		SchemaVersion: jsonschema.SchemaVersion,
		Kind:          kind,
		Payload:       json.RawMessage(payloadBytes),
	}
	out, _ := json.Marshal(env)
	return string(out)
}

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
	Driver []string `arg:"positional,required" help:"One or more drivers to add, optionally with a version constraint (for example: mysql, mysql=0.1.0, mysql>=1,<2)"`
	Path   string   `arg:"-p" placeholder:"FILE" default:"./dbc.toml" help:"Driver list to add to"`
	Pre    bool     `arg:"--pre" help:"Allow pre-release versions implicitly"`
	Json   bool     `arg:"--json" help:"Print output as JSON instead of plaintext"`
}

func (c AddCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return addModel{
		baseModel:  baseModel,
		Driver:     c.Driver,
		Path:       c.Path,
		Pre:        c.Pre,
		jsonOutput: c.Json,
	}
}

func (c AddCmd) GetModel() tea.Model {
	return addModel{
		Driver:     c.Driver,
		Path:       c.Path,
		Pre:        c.Pre,
		jsonOutput: c.Json,
		baseModel:  defaultBaseModel(),
	}
}

type addDoneMsg struct {
	result       string
	resolvedPath string
}

type addModel struct {
	baseModel

	Driver       []string
	Path         string
	Pre          bool
	jsonOutput   bool
	list         DriversList
	result       string
	resolvedPath string
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

		lockPath := filepath.Join(filepath.Dir(p), ".dbc.project.lock")
		lock, err := fslock.Acquire(lockPath, 10*time.Second)
		if err != nil {
			return fmt.Errorf("another dbc operation is in progress: %w", err)
		}
		defer lock.Release()

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
				return wrapWithRegistryContext(err, registryErrors)
			}

			if spec.Vers != nil {
				spec.Vers.IncludePrerelease = m.Pre
				_, err = drv.GetWithConstraint(spec.Vers, config.PlatformTuple())
				if err != nil {
					return fmt.Errorf("error getting driver: %w", err)
				}
			} else {
				if !m.Pre && !drv.HasNonPrerelease() {
					var err error
					if len(drv.PkgInfo) > 0 {
						// Has packages, but they're all prereleases
						err = fmt.Errorf("driver `%s` not found in driver registry index (but prerelease versions filtered out); try: dbc add --pre %s", spec.Name, spec.Name)
					} else {
						// No packages. Very unlikely edge case.
						err = fmt.Errorf("driver `%s` not found in driver registry index", spec.Name)
					}
					if registryErrors != nil {
						return wrapWithRegistryContext(err, registryErrors)
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

		wf, err := os.Create(p)
		if err != nil {
			return fmt.Errorf("error creating file %s: %w", p, err)
		}
		defer wf.Close()

		if err := toml.NewEncoder(wf).Encode(m.list); err != nil {
			return err
		}
		result += "\nuse `dbc sync` to install the drivers in the list"
		return addDoneMsg{result: result, resolvedPath: p}
	}
}

func (m addModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case addDoneMsg:
		m.result = msg.result
		m.resolvedPath = msg.resolvedPath
		return m, tea.Quit
	case string:
		m.result = msg
		return m, tea.Quit
	default:
		bm, cmd := m.baseModel.Update(msg)
		m.baseModel = bm.(baseModel)

		return m, cmd
	}
}

func (m addModel) IsJSONMode() bool { return m.jsonOutput }

func (m addModel) FinalOutput() string {
	if m.status != 0 {
		if m.jsonOutput {
			return marshalEnvelope("error", jsonschema.ErrorResponse{
				Code:    "add_failed",
				Message: m.err.Error(),
			})
		}
		return ""
	}
	if m.jsonOutput {
		drivers := make([]jsonschema.AddResponseDriver, 0, len(m.Driver))
		for _, d := range m.Driver {
			driverName, constraint, _ := parseDriverConstraint(d)
			var constraintStr string
			if constraint != nil {
				constraintStr = constraint.String()
			}
			drivers = append(drivers, jsonschema.AddResponseDriver{
				Name:              driverName,
				VersionConstraint: constraintStr,
			})
		}
		return marshalEnvelope("add.response", jsonschema.AddResponse{
			DriverListPath: m.resolvedPath,
			Drivers:        drivers,
		})
	}
	return m.result
}

func (m addModel) View() tea.View { return tea.NewView("") }
