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
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/columnar-tech/dbc"
	"github.com/mattn/go-isatty"
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

	driver   string
	ready    bool
	viewport viewport.Model
	drv      dbc.Driver
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
	var cmds []tea.Cmd
	base, cmd := m.baseModel.Update(msg)
	m.baseModel = base.(baseModel)
	cmds = append(cmds, cmd)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" {
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		marginHeight := lipgloss.Height(": ")
		if m.viewport.Width == 0 {
			m.viewport = viewport.New(msg.Width, msg.Height-marginHeight)
			m.viewport.YPosition = 0
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - marginHeight
		}
	case dbc.Driver:
		if isatty.IsTerminal(os.Stdout.Fd()) {
			cmds = append(cmds, tea.EnterAltScreen)
			m.viewport.SetContent(formatDriverInfo(msg))
		} else {
			m.drv = msg
			cmds = append(cmds, tea.Quit)
		}
		m.ready = true
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m infoModel) FinalOutput() string {
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return formatDriverInfo(m.drv)
	}
	return ""
}

func (m infoModel) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	var suffix string
	if isatty.IsTerminal(os.Stdout.Fd()) {
		suffix = "\n: "
		if m.viewport.AtBottom() {
			suffix = "\n" + lipgloss.NewStyle().
				Background(lipgloss.Color("240")).Render("(END)")
		}
	}

	return m.viewport.View() + suffix
}
