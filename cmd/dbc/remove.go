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
	"errors"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pelletier/go-toml/v2"
)

type RemoveCmd struct {
	Driver string `arg:"positional,required" help:"Driver to remove"`
	Path   string `arg:"-p" placeholder:"FILE" default:"./dbc.toml" help:"Driver list to remove from"`
}

func (c RemoveCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return removeModel{
		baseModel: baseModel,
		Driver:    c.Driver,
		Path:      c.Path,
	}
}

func (c RemoveCmd) GetModel() tea.Model {
	return removeModel{
		Driver: c.Driver,
		Path:   c.Path,
		baseModel: baseModel{
			getDriverRegistry: getDriverRegistry,
			downloadPkg:       downloadPkg,
		},
	}
}

type removeModel struct {
	baseModel

	Driver string
	Path   string

	list DriversList
}

func (m removeModel) Init() tea.Cmd {
	return func() tea.Msg {
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

		m.Driver = strings.TrimSpace(m.Driver)
		if m.list.Drivers == nil {
			return fmt.Errorf("no drivers found in %s", p)
		}

		_, ok := m.list.Drivers[m.Driver]
		if !ok {
			return fmt.Errorf("driver '%s' not found in %s", m.Driver, p)
		}

		delete(m.list.Drivers, m.Driver)

		f, err = os.Create(p)
		if err != nil {
			return fmt.Errorf("error creating file %s: %w", p, err)
		}
		defer f.Close()

		if err := toml.NewEncoder(f).Encode(m.list); err != nil {
			return err
		}

		return fmt.Sprintf("removed '%s' from driver list", m.Driver)
	}
}

func (m removeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case string:
		return m, tea.Sequence(tea.Println(msg), tea.Quit)
	default:
		bm, cmd := m.baseModel.Update(msg)
		m.baseModel = bm.(baseModel)

		return m, cmd
	}
}

func (m removeModel) View() string { return "" }
