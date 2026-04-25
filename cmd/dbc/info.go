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
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

type InfoCmd struct {
	Driver string `arg:"positional,required" help:"Driver to get info about"`
	Json   bool   `help:"Print output as JSON instead of plaintext"`
}

func (c InfoCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return infoModel{
		baseModel:  baseModel,
		jsonOutput: c.Json,
		driver:     c.Driver,
	}
}

func (c InfoCmd) GetModel() tea.Model {
	return c.GetModelCustom(defaultBaseModel())
}

type infoModel struct {
	baseModel

	driver     string
	jsonOutput bool
	drv        dbc.Driver
}

func (m infoModel) Init() tea.Cmd {
	return func() tea.Msg {
		drivers, registryErr := m.getDriverRegistry()
		// If we have no drivers and there's an error, fail immediately
		if len(drivers) == 0 && registryErr != nil {
			return fmt.Errorf("error getting driver list: %w", registryErr)
		}

		drv, err := findDriver(m.driver, drivers)
		if err != nil {
			return wrapWithRegistryContext(err, registryErr)
		}

		return drv
	}
}

func formatDriverInfo(drv dbc.Driver) string {
	if len(drv.PkgInfo) == 0 {
		return ""
	}

	info, ok := drv.MaxVersion()
	if !ok {
		return ""
	}
	var b strings.Builder

	b.WriteString(bold.Render("Driver: ") + nameStyle.Render(drv.Path) + "\n")
	b.WriteString(bold.Render("Version: ") + info.Version.String() + "\n")
	b.WriteString(bold.Render("Title: ") + drv.Title + "\n")
	b.WriteString(bold.Render("License: ") + drv.License + "\n")
	b.WriteString(bold.Render("Description: ") + drv.Desc + "\n")
	b.WriteString(bold.Render("Available Packages:") + "\n")
	for _, pkg := range info.Packages {
		b.WriteString("   - " + descStyle.Render(pkg.Platform) + "\n")
	}

	return strings.TrimSuffix(b.String(), "\n")
}

func driverInfoJSON(drv dbc.Driver) string {
	info, ok := drv.MaxVersion()
	if !ok {
		return "{}"
	}

	driverInfo := jsonschema.DriverInfo{
		Driver:      drv.Path,
		Version:     info.Version.String(),
		Title:       drv.Title,
		License:     drv.License,
		Description: drv.Desc,
	}
	for _, pkg := range info.Packages {
		driverInfo.Packages = append(driverInfo.Packages, pkg.Platform)
	}

	payloadBytes, err := json.Marshal(driverInfo)
	if err != nil {
		return err.Error()
	}
	env := jsonschema.Envelope{
		SchemaVersion: jsonschema.SchemaVersion,
		Kind:          "driver.info",
		Payload:       json.RawMessage(payloadBytes),
	}
	jsonOutput, err := json.Marshal(env)
	if err != nil {
		return err.Error()
	}
	return string(jsonOutput)
}

func (m infoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dbc.Driver:
		m.drv = msg
		return m, tea.Quit
	default:
		bm, cmd := m.baseModel.Update(msg)
		m.baseModel = bm.(baseModel)
		return m, cmd
	}
}

func (m infoModel) IsJSONMode() bool { return m.jsonOutput }

func (m infoModel) FinalOutput() string {
	if m.jsonOutput {
		return driverInfoJSON(m.drv)
	}
	return formatDriverInfo(m.drv)
}

func (m infoModel) View() tea.View {
	return tea.NewView("")
}
