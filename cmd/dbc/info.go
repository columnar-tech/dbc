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
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc"
)

type InfoCmd struct {
	Driver string `arg:"positional,required" help:"Driver to get info about"`
}

func (c InfoCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return infoModel{
		baseModel: baseModel,
		driver:    c.Driver,
	}
}

func (c InfoCmd) GetModel() tea.Model {
	return c.GetModelCustom(baseModel{
		getDriverList: getDriverList,
		downloadPkg:   downloadPkg,
	})
}

type infoModel struct {
	baseModel

	driver string
	drv    dbc.Driver
}

func (m infoModel) Init() tea.Cmd {
	return func() tea.Msg {
		drivers, err := m.getDriverList()
		if err != nil {
			return err
		}

		drv, err := findDriver(m.driver, drivers)
		if err != nil {
			return err
		}

		return drv
	}
}

func formatDriverInfo(drv dbc.Driver) string {
	if len(drv.PkgInfo) == 0 {
		return ""
	}

	info := drv.MaxVersion()
	var b strings.Builder

	b.WriteString(bold.Render("Driver: ") + nameStyle.Render(drv.Path) + "\n")
	b.WriteString(bold.Render("Version: ") + info.Version.String() + "\n")
	b.WriteString(bold.Render("Title: ") + drv.Title + "\n")
	b.WriteString(bold.Render("License: ") + drv.License + "\n")
	b.WriteString(bold.Render("Description: ") + drv.Desc + "\n")
	b.WriteString(bold.Render("Available Packages:") + "\n")
	for _, pkg := range info.Packages {
		b.WriteString("   - " + descStyle.Render(pkg.PlatformTuple) + "\n")
	}

	return b.String()
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

func (m infoModel) FinalOutput() string {
	return formatDriverInfo(m.drv)
}

func (m infoModel) View() string {
	return ""
}
