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
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/columnar-tech/dbc/config"
)

type driverDidUninstallMsg struct{}

type UninstallCmd struct {
	Driver string             `arg:"positional,required" help:"Driver to uninstall"`
	Level  config.ConfigLevel `arg:"-l" help:"Config level to uninstall from (user, system)"`
}

func (c UninstallCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return uninstallModel{
		baseModel: baseModel,
		Driver:    c.Driver,
		cfg:       getConfig(c.Level),
	}
}

func (c UninstallCmd) GetModel() tea.Model {
	return uninstallModel{
		baseModel: baseModel{
			getDriverList: getDriverList,
			downloadPkg:   downloadPkg,
		},
		Driver: c.Driver,
		cfg:    getConfig(c.Level),
	}
}

type uninstallModel struct {
	baseModel

	Driver string
	cfg    config.Config
}

func (m uninstallModel) Init() tea.Cmd {
	return m.startUninstall
}

func (m uninstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmds := make([]tea.Cmd, 0)
	switch msg := msg.(type) {
	case config.DriverInfo:
		return m.performUninstall(msg)
	case driverDidUninstallMsg:
		return m, tea.Sequence(tea.Printf("Driver `%s` uninstalled successfully!\n", m.Driver), tea.Quit)
	case error:
		m.status = 1
		return m, tea.Sequence(
			tea.Println(errStyle.Render("Error: "+msg.Error())),
			tea.Quit)
	}

	return m, tea.Sequence(cmds...)
}

func (m uninstallModel) View() string {
	return ""
}

func (m uninstallModel) startUninstall() tea.Msg {
	info, err := config.GetDriver(m.cfg, m.Driver)
	if err != nil {
		return fmt.Errorf("failed to find driver `%s` in order to uninstall it: %v", m.Driver, err)
	}

	return info
}

func (m uninstallModel) performUninstall(driver config.DriverInfo) (tea.Model, tea.Cmd) {
	return m, func() tea.Msg {
		err := config.UninstallDriver(m.cfg, driver)
		if err != nil {
			return fmt.Errorf("failed to uninstall driver: %v", err)
		}
		return driverDidUninstallMsg{}
	}
}
