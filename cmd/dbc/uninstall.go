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
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

type driverDidUninstallMsg struct{}

type UninstallCmd struct {
	Driver string             `arg:"positional,required" help:"Driver to uninstall"`
	Level  config.ConfigLevel `arg:"-l" help:"Config level to uninstall from (user, system)"`
	Json   bool               `arg:"--json" help:"Print output as JSON instead of plaintext"`
}

func (c UninstallCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return uninstallModel{
		baseModel:  baseModel,
		Driver:     c.Driver,
		cfg:        getConfig(c.Level),
		jsonOutput: c.Json,
	}
}

func (c UninstallCmd) GetModel() tea.Model {
	return uninstallModel{
		baseModel:  defaultBaseModel(),
		Driver:     c.Driver,
		cfg:        getConfig(c.Level),
		jsonOutput: c.Json,
	}
}

type uninstallModel struct {
	baseModel

	Driver     string
	cfg        config.Config
	jsonOutput bool
}

func (m uninstallModel) Init() tea.Cmd {
	return func() tea.Msg {
		installDir := "."
		if locs := filepath.SplitList(m.cfg.Location); len(locs) > 0 && locs[0] != "" {
			installDir = locs[0]
		}
		lockDir := installDir
		for {
			if _, err := os.Stat(lockDir); err == nil {
				break
			}
			parent := filepath.Dir(lockDir)
			if parent == lockDir {
				lockDir = os.TempDir()
				break
			}
			lockDir = parent
		}
		lockPath := filepath.Join(lockDir, ".dbc.install.lock")
		lock, err := fslock.Acquire(lockPath, 10*time.Second)
		if err != nil {
			return fmt.Errorf("another dbc operation is in progress: %w", err)
		}
		defer lock.Release()
		return m.startUninstall()
	}
}

func (m uninstallModel) IsJSONMode() bool { return m.jsonOutput }

func (m uninstallModel) FinalOutput() string {
	if m.status != 0 {
		return ""
	}

	if m.jsonOutput {
		payload := jsonschema.UninstallStatus{Status: "success", Driver: m.Driver}
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return fmt.Sprintf(`{"schema_version":1,"kind":"error","payload":{"code":"marshal_error","message":"%s"}}`, err.Error())
		}
		env := jsonschema.Envelope{
			SchemaVersion: jsonschema.SchemaVersion,
			Kind:          "uninstall.status",
			Payload:       json.RawMessage(payloadBytes),
		}
		jsonOutput, err := json.Marshal(env)
		if err != nil {
			return fmt.Sprintf(`{"schema_version":1,"kind":"error","payload":{"code":"marshal_error","message":"%s"}}`, err.Error())
		}
		return string(jsonOutput)
	}
	return fmt.Sprintf("Driver `%s` uninstalled successfully!", m.Driver)
}

func (m uninstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmds := make([]tea.Cmd, 0)
	switch msg := msg.(type) {
	case config.DriverInfo:
		return m.performUninstall(msg)
	case driverDidUninstallMsg:
		return m, tea.Quit
	case error:
		m.status = 1
		m.err = msg
		if m.jsonOutput {
			return m, tea.Sequence(tea.Println(marshalEnvelope("error", jsonschema.ErrorResponse{
				Code:    "uninstall_failed",
				Message: msg.Error(),
			})), tea.Quit)
		}
		return m, tea.Quit
	}

	return m, tea.Sequence(cmds...)
}

func (m uninstallModel) View() tea.View {
	return tea.NewView("")
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
