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
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/pelletier/go-toml/v2"
)

type RemoveCmd struct {
	Driver string `arg:"positional,required" help:"Driver to remove"`
	Path   string `arg:"-p" placeholder:"FILE" default:"./dbc.toml" help:"Driver list to remove from"`
	Json   bool   `arg:"--json" help:"Output JSON instead of plaintext"`
}

func (c RemoveCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return removeModel{
		baseModel:  baseModel,
		Driver:     c.Driver,
		Path:       c.Path,
		jsonOutput: c.Json,
	}
}

func (c RemoveCmd) GetModel() tea.Model {
	return removeModel{
		Driver:     c.Driver,
		Path:       c.Path,
		jsonOutput: c.Json,
		baseModel:  defaultBaseModel(),
	}
}

type removeDoneMsg struct {
	result      string
	resolvedPath string
}

type removeModel struct {
	baseModel

	Driver     string
	Path       string
	jsonOutput bool

	list        DriversList
	result      string
	resolvedPath string
}

func (m removeModel) Init() tea.Cmd {
	return func() tea.Msg {
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

		m.Driver = strings.TrimSpace(m.Driver)
		if m.list.Drivers == nil {
			return fmt.Errorf("no drivers found in %s", p)
		}

		_, ok := m.list.Drivers[m.Driver]
		if !ok {
			return fmt.Errorf("driver '%s' not found in %s", m.Driver, p)
		}

		delete(m.list.Drivers, m.Driver)

		wf, err := os.Create(p)
		if err != nil {
			return fmt.Errorf("error creating file %s: %w", p, err)
		}
		defer wf.Close()

		if err := toml.NewEncoder(wf).Encode(m.list); err != nil {
			return err
		}

		return removeDoneMsg{result: fmt.Sprintf("removed '%s' from driver list", m.Driver), resolvedPath: p}
	}
}

func (m removeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case removeDoneMsg:
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

func (m removeModel) IsJSONMode() bool { return m.jsonOutput }

func (m removeModel) FinalOutput() string {
	if m.status != 0 {
		return ""
	}
	if m.jsonOutput {
		return marshalEnvelope("remove.response", jsonschema.RemoveResponse{
			DriverListPath: m.resolvedPath,
			Driver:         jsonschema.RemoveResponseDriver{Name: strings.TrimSpace(m.Driver)},
		})
	}
	return m.result
}

func (m removeModel) View() tea.View { return tea.NewView("") }
