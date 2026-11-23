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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/pelletier/go-toml/v2"
)

var msgStyle = lipgloss.NewStyle().Faint(true)

func marshalEnvelope(kind string, payload any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // don't escape <, >, & in JSON output
	enc.Encode(payload)

	env := jsonschema.Envelope{
		SchemaVersion: jsonschema.SchemaVersion,
		Kind:          kind,
		Payload:       json.RawMessage(b.Bytes()),
	}

	b.Reset()
	enc.Encode(env)
	return b.String()
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
	// Registry is stored as the source of every driver this command adds.
	Registry string `arg:"--registry" placeholder:"NAME|URL" help:"Take the drivers only from this registry and record it as their source: the name of a [[registries]] entry in the driver list, or the URL of a configured registry. Without it, a driver keeps the source it already has"`
	Json     bool   `arg:"--json" help:"Print output as JSON instead of plaintext"`
}

func (c AddCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return addModel{
		baseModel:  baseModel,
		Driver:     c.Driver,
		Path:       c.Path,
		Pre:        c.Pre,
		Registry:   c.Registry,
		jsonOutput: c.Json,
	}
}

func (c AddCmd) GetModel() tea.Model {
	return addModel{
		Driver:     c.Driver,
		Path:       c.Path,
		Pre:        c.Pre,
		Registry:   c.Registry,
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
	Registry     string
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
		p, err := driverListPath(m.Path)
		if err != nil {
			return err
		}

		// Take the project lock briefly to read a consistent snapshot of
		// dbc.toml, then release it before the registry lookup so a slow
		// network fetch doesn't hold the lock. The lock is reacquired
		// below for the final merge/write phase. Without this short-lock
		// read, a concurrent writer using os.Create could expose partial
		// contents to this decode path.
		lockPath := filepath.Join(filepath.Dir(p), ".dbc.project.lock")
		readLock, err := acquireLock(lockPath, 10*time.Second)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			readLock.Release()
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("error opening driver list: %s doesn't exist\nDid you run `dbc init`?", m.Path)
			}
			return fmt.Errorf("error opening driver list at %s: %w", m.Path, err)
		}
		if m.list, err = decodeDriverList(f, p); err != nil {
			f.Close()
			readLock.Release()
			return err
		}
		f.Close()
		readLock.Release()

		if err := applyProjectRegistries(m.list); err != nil {
			return err
		}
		if warning := projectRegistryWarning(m.list); warning != "" {
			fmt.Fprintln(os.Stderr, warning)
		}

		if err := validateDriverSources(m.list); err != nil {
			return err
		}
		flagSource := registrySourceFlag(m.Registry)
		if flagSource != nil {
			if _, err := flagSource.target(m.list.Registries); err != nil {
				return fmt.Errorf("--registry: %w", err)
			}
		}

		// The merged index is only fetched for drivers without a source.
		var (
			merged         []dbc.Driver
			registryErrors error
			mergedLoaded   bool
		)
		mergedIndex := func() ([]dbc.Driver, error) {
			if !mergedLoaded {
				mergedLoaded = true
				merged, registryErrors = m.getDriverRegistry()
			}
			// If we have no drivers and there's an error, fail immediately.
			// Otherwise registry errors are kept to explain a missing driver.
			if len(merged) == 0 && registryErrors != nil {
				return nil, fmt.Errorf("error getting driver list: %w", registryErrors)
			}
			return merged, nil
		}
		sourceIndexes := map[string][]dbc.Driver{}
		sourceIndex := func(name string, target sourceTarget) ([]dbc.Driver, error) {
			if drivers, ok := sourceIndexes[target.identity]; ok {
				return drivers, nil
			}
			registries, err := m.getRegistries()
			if err != nil {
				return nil, err
			}
			reg, err := target.bind(registries)
			if err != nil {
				return nil, fmt.Errorf("driver %s: %w", name, err)
			}
			drivers, err := m.getRegistryIndex(reg)
			if err != nil {
				return nil, fmt.Errorf("could not fetch the index of registry %s, which driver %s is taken from: %w",
					target.identity, name, redactRegistryError(err, m.getRegistries, reg.BaseURL))
			}
			sourceIndexes[target.identity] = drivers
			return drivers, nil
		}

		if m.list.Drivers == nil {
			m.list.Drivers = make(map[string]driverSpec)
		}
		// previous holds the source of each driver as read, to detect a
		// concurrent change of it before writing.
		previous := map[string]*driverSource{}

		var result string
		for i, spec := range specs {
			if i != 0 {
				result += "\n"
			}

			requested := driverSpec{Version: spec.Vers}
			if m.Pre {
				requested.Prerelease = "allow"
			}
			requested = requested.withPrerelease()
			previous[spec.Name] = m.list.Drivers[spec.Name].Source
			requested.Source = flagSource
			if requested.Source == nil {
				requested.Source = previous[spec.Name]
			}

			var drivers []dbc.Driver
			var target *sourceTarget
			if requested.Source != nil {
				t, err := requested.Source.target(m.list.Registries)
				if err != nil {
					return fmt.Errorf("driver %s: %w", spec.Name, err)
				}
				target = &t
				drivers, err = sourceIndex(spec.Name, t)
				if err != nil {
					return err
				}
			} else if drivers, err = mergedIndex(); err != nil {
				return err
			}
			pkg, err := dbc.ResolvePackage(drivers, spec.Name, func(candidate *dbc.Driver) (dbc.PkgInfo, error) {
				return selectProjectPackage(candidate, requested, nil, config.PlatformTuple())
			})
			if err != nil && target != nil {
				if pkg.Driver.Path == "" {
					return fmt.Errorf("driver `%s` not found in %s (%s)", spec.Name, target.source, target.identity)
				}
				return fmt.Errorf("error getting driver %s from %s: %w", spec.Name, target.source, redactRegistryError(err, m.getRegistries))
			}
			if err != nil {
				err = redactRegistryError(err, m.getRegistries)
				switch {
				case pkg.Driver.Path == "":
					return wrapWithRegistryContext(err, registryErrors)
				case spec.Vers != nil:
					return fmt.Errorf("error getting driver: %w", err)
				case errors.Is(err, dbc.ErrPackageMiss) && !m.Pre && len(pkg.Driver.PkgInfo) > 0 && !pkg.Driver.HasNonPrerelease():
					err = fmt.Errorf("driver `%s` not found in driver registry index (but prerelease versions filtered out); try: dbc add --pre %s", spec.Name, spec.Name)
				case errors.Is(err, dbc.ErrPackageMiss) && !m.Pre:
					err = fmt.Errorf("driver `%s` not found in driver registry index", spec.Name)
				}
				return wrapWithRegistryContext(err, registryErrors)
			}

			current, ok := m.list.Drivers[spec.Name]
			m.list.Drivers[spec.Name] = requested

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
			if requested.Source != nil {
				result += nameStyle.Render(" from", requested.Source.String())
			}
		}

		// Reacquire the project lock for the read-modify-write phase.
		// Re-read the file under the lock so a concurrent
		// `dbc add`/`dbc remove` that landed while we were doing the
		// registry lookup above doesn't get clobbered.
		lock, err := acquireLock(lockPath, 10*time.Second)
		if err != nil {
			return err
		}
		defer lock.Release()

		var current DriversList
		if rf, err := os.Open(p); err == nil {
			var decodeErr error
			if current, decodeErr = decodeDriverList(rf, p); decodeErr != nil {
				rf.Close()
				return fmt.Errorf("error re-reading driver list under lock: %w", decodeErr)
			}
			rf.Close()
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("error re-reading driver list at %s: %w", m.Path, err)
		}

		// If the registry configuration changed while the (unlocked)
		// registry lookup was in flight, the drivers we just validated
		// may not exist under the new registry set. Abort so the caller
		// can retry against the new configuration rather than write a
		// potentially-inconsistent dbc.toml.
		if registriesChanged(m.list, current) {
			return fmt.Errorf("dbc.toml registry configuration changed while resolving drivers; please retry `dbc add`")
		}
		// A driver's source, or the registry a source name refers to, may
		// also have changed: the drivers were validated against the old one.
		for _, spec := range specs {
			if flagSource == nil && !previous[spec.Name].equal(current.Drivers[spec.Name].Source) {
				return fmt.Errorf("the source of driver %s in dbc.toml changed while resolving drivers; please retry `dbc add`", spec.Name)
			}
			if src := m.list.Drivers[spec.Name].Source; src != nil && !sameSourceTarget(*src, m.list.Registries, current.Registries) {
				return fmt.Errorf("the registry that driver %s is taken from changed in dbc.toml while resolving drivers; please retry `dbc add`", spec.Name)
			}
		}

		// Merge ONLY the driver entries this invocation actually added or
		// replaced onto whatever is on disk. Leaving untouched drivers,
		// registries, and exclude_builtin_registries alone preserves concurrent
		// `dbc remove`/`dbc add` edits that landed while we were doing
		// the unlocked registry lookup above.
		if current.Drivers == nil {
			current.Drivers = make(map[string]driverSpec)
		}
		for _, spec := range specs {
			current.Drivers[spec.Name] = m.list.Drivers[spec.Name]
		}

		wf, err := os.Create(p)
		if err != nil {
			return fmt.Errorf("error creating file %s: %w", p, err)
		}
		defer wf.Close()

		if err := toml.NewEncoder(wf).Encode(current); err != nil {
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
