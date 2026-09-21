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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/columnar-tech/dbc/internal/packslip"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceresolution"
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

// exactSourceVersionArgument extracts an exact version from the original add
// argument without allowing Masterminds' permissive constraint parser to
// coerce non-SemVer spellings. The CLI's equality operator is optional after
// the driver/version separator.
func exactSourceVersionArgument(input string, required bool) (string, error) {
	input = strings.TrimSpace(input)
	split := strings.IndexAny(input, " ~^<>=!")
	if split < 0 {
		if required {
			return "", errors.New("an exact version is required")
		}
		return "", nil
	}
	version := strings.TrimSpace(input[split:])
	if strings.HasPrefix(version, "=") {
		version = strings.TrimSpace(strings.TrimPrefix(version, "="))
	}
	parsed, err := semver.StrictNewVersion(version)
	if err != nil || parsed.String() != version {
		return "", fmt.Errorf("%q is not an exact canonical SemVer 2.0.0 version", version)
	}
	return version, nil
}

func exactSourceVersionConstraint(version string) (*semver.Constraints, error) {
	parsed, err := semver.StrictNewVersion(version)
	if err != nil || parsed.String() != version {
		return nil, fmt.Errorf("%q is not an exact canonical SemVer 2.0.0 version", version)
	}
	return semver.NewConstraint(version)
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

type addTargetSnapshot struct {
	present bool
	source  *dbc.DriverSource
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
		Name  string
		Input string
		Vers  *semver.Constraints
	}

	var specs []driverInput
	for _, d := range m.Driver {
		driverName, vers, err := parseDriverConstraint(d)
		if err != nil {
			return errCmd("invalid driver constraint '%s': %w", d, err)
		}

		specs = append(specs, driverInput{Name: driverName, Input: d, Vers: vers})
	}

	return func() tea.Msg {
		p, err := driverListPath(m.Path)
		if err != nil {
			return err
		}

		// Take the project lock briefly to read a consistent snapshot of
		// dbc.toml, then release it before source resolution so slow
		// registry/network or local package I/O doesn't hold the lock. The lock is reacquired
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
		if err := toml.NewDecoder(f).Decode(&m.list); err != nil {
			f.Close()
			readLock.Release()
			return err
		}
		if err := m.list.validateSources(); err != nil {
			f.Close()
			readLock.Release()
			return err
		}
		originalTargets := make(map[string]addTargetSnapshot, len(specs))
		for _, spec := range specs {
			original, present := m.list.Drivers[spec.Name]
			snapshot := addTargetSnapshot{present: present}
			if original.Source != nil {
				sourceCopy := *original.Source
				snapshot.source = &sourceCopy
			}
			originalTargets[spec.Name] = snapshot
		}
		f.Close()
		readLock.Release()

		if m.list.Drivers == nil {
			m.list.Drivers = make(map[string]driverSpec)
		}
		needsRegistry := false
		for _, spec := range specs {
			current := m.list.Drivers[spec.Name]
			if current.Source == nil || current.Source.Type == dbc.DriverSourceRegistry {
				needsRegistry = true
			}
		}
		var drivers []dbc.Driver
		var registryErrors error
		if needsRegistry {
			if err := applyProjectRegistries(m.list); err != nil {
				return err
			}
			drivers, registryErrors = m.getDriverRegistry()
			// If a registry-backed item needs lookup and no registry is
			// available, preserve the existing add error contract.
			if len(drivers) == 0 && registryErrors != nil {
				return fmt.Errorf("error getting driver list: %w", registryErrors)
			}
		}

		var result string
		var packslipResolver packslip.Resolver
		for i, spec := range specs {
			if i != 0 {
				result += "\n"
			}

			current, ok := m.list.Drivers[spec.Name]
			updatedVersion := spec.Vers
			switch {
			case current.Source != nil && current.Source.Type == dbc.DriverSourcePackslip:
				if m.Pre {
					return fmt.Errorf("driver %q Packslip source does not support `dbc add --pre`", spec.Name)
				}
				versionText, err := exactSourceVersionArgument(spec.Input, true)
				if err != nil {
					return fmt.Errorf("driver %q Packslip source requires an exact SemVer 2.0.0 version: %w", spec.Name, err)
				}
				updatedVersion, err = exactSourceVersionConstraint(versionText)
				if err != nil {
					return fmt.Errorf("driver %q Packslip source has an invalid exact version: %w", spec.Name, err)
				}
				if packslipResolver == nil {
					if m.newPackslipResolver == nil {
						return errors.New("no Packslip resolver is configured")
					}
					packslipResolver, err = m.newPackslipResolver()
					if err != nil {
						return fmt.Errorf("create Packslip resolver: %w", err)
					}
				}
				candidateSpec := current
				candidateSpec.Version = updatedVersion
				requirement, err := requirementForDriverSpec(spec.Name, candidateSpec)
				if err != nil {
					return err
				}
				release, err := sourceresolution.ResolvePackslip(
					context.Background(), packslipResolver, current.Source.Project, spec.Name, versionText)
				if err != nil {
					return fmt.Errorf("error resolving Packslip source: %w", err)
				}
				if _, err := requirement.ValidateResolverResult(release); err != nil {
					return fmt.Errorf("Packslip resolver returned an invalid release: %w", err)
				}
			case current.Source != nil && current.Source.Type == dbc.DriverSourcePath:
				if m.Pre {
					return fmt.Errorf("driver %q path source does not support `dbc add --pre`", spec.Name)
				}
				versionText, err := exactSourceVersionArgument(spec.Input, false)
				if err != nil {
					return fmt.Errorf("driver %q path source requires an exact SemVer 2.0.0 version when specified: %w", spec.Name, err)
				}
				if versionText == "" {
					updatedVersion = nil
				} else {
					updatedVersion, err = exactSourceVersionConstraint(versionText)
					if err != nil {
						return fmt.Errorf("driver %q path source has an invalid exact version: %w", spec.Name, err)
					}
				}
				candidateSpec := current
				candidateSpec.Version = updatedVersion
				requirement, err := requirementForDriverSpec(spec.Name, candidateSpec)
				if err != nil {
					return err
				}
				target, err := resolution.TargetFromPlatformTuple(config.PlatformTuple())
				if err != nil {
					return fmt.Errorf("unsupported add platform: %w", err)
				}
				release, err := sourceresolution.ResolvePath(context.Background(), current.Source.Path, sourceresolution.Request{
					DriverID: spec.Name, Version: versionText, Target: target,
					Platform: config.PlatformTuple(), BaseDir: filepath.Dir(p),
				})
				if err != nil {
					return fmt.Errorf("error resolving path source: %w", err)
				}
				if _, err := requirement.ValidateResolverResult(release); err != nil {
					return fmt.Errorf("path resolver returned an invalid package: %w", err)
				}
			default:
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
				} else if !m.Pre && !drv.HasNonPrerelease() {
					var err error
					if len(drv.PkgInfo) > 0 {
						err = fmt.Errorf("driver `%s` not found in driver registry index (but prerelease versions filtered out); try: dbc add --pre %s", spec.Name, spec.Name)
					} else {
						err = fmt.Errorf("driver `%s` not found in driver registry index", spec.Name)
					}
					if registryErrors != nil {
						return wrapWithRegistryContext(err, registryErrors)
					}
					return err
				}
			}

			updated := current
			updated.Version = updatedVersion
			updated.Prerelease = ""
			if m.Pre {
				updated.Prerelease = "allow"
			}
			m.list.Drivers[spec.Name] = updated

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
			if decodeErr := toml.NewDecoder(rf).Decode(&current); decodeErr != nil {
				rf.Close()
				return fmt.Errorf("error re-reading driver list under lock: %w", decodeErr)
			}
			rf.Close()
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("error re-reading driver list at %s: %w", m.Path, err)
		}
		if err := current.validateSources(); err != nil {
			return fmt.Errorf("error re-reading driver list under lock: %w", err)
		}

		// If registry configuration changed while a registry-backed
		// selection was being validated, abort rather than write against a
		// potentially different registry set.
		if needsRegistry && registriesChanged(m.list, current) {
			return fmt.Errorf("dbc.toml registry configuration changed while resolving drivers; please retry `dbc add`")
		}

		// Merge ONLY the driver entries this invocation actually added or
		// replaced onto whatever is on disk. Leaving untouched drivers,
		// registries, and replace_defaults alone preserves concurrent
		// `dbc remove`/`dbc add` edits that landed while source resolution
		// was in flight.
		if current.Drivers == nil {
			current.Drivers = make(map[string]driverSpec)
		}
		for _, spec := range specs {
			updated := m.list.Drivers[spec.Name]
			latest, present := current.Drivers[spec.Name]
			original := originalTargets[spec.Name]
			if original.present != present {
				return fmt.Errorf("driver %q entry presence changed while resolving drivers; please retry `dbc add`", spec.Name)
			}
			if !sameDriverSourceIdentity(original.source, latest.Source) {
				return fmt.Errorf("driver %q source changed while resolving drivers; please retry `dbc add`", spec.Name)
			}
			if latest.Source != nil {
				// `add` only changes the version selection. Preserve the source
				// value from the locked re-read after checking it still matches
				// the source against which the version was validated.
				updated.Source = latest.Source
			}
			current.Drivers[spec.Name] = updated
		}
		if err := current.validateSources(); err != nil {
			return fmt.Errorf("updated driver list is invalid: %w", err)
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
