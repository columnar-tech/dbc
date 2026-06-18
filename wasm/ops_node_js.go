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

//go:build js && dbcnode

package main

import (
	"context"
	"syscall/js"

	"github.com/columnar-tech/dbc/config"
)

func registerNodeOps() {
	js.Global().Set("dbcInstall", promisify(jsInstall))
	js.Global().Set("dbcList", promisify(jsList))
	js.Global().Set("dbcUninstall", promisify(jsUninstall))
}

type manifestDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Source     string `json:"source"`
	DriverPath string `json:"driverPath"`
}

func jsInstall(args []js.Value) func() (any, error) {
	name := args[0].String()
	location := args[1].String()
	return func() (any, error) {
		c, err := newClient()
		if err != nil {
			return nil, err
		}
		m, err := c.Install(context.Background(), config.Config{Level: config.ConfigEnv, Location: location}, name)
		if err != nil {
			return nil, err
		}
		version := ""
		if m.DriverInfo.Version != nil {
			version = m.DriverInfo.Version.String()
		}
		return toJSONValue(manifestDTO{
			ID:         m.DriverInfo.ID,
			Name:       m.DriverInfo.Name,
			Version:    version,
			Source:     m.DriverInfo.Source,
			DriverPath: m.DriverInfo.Driver.Shared.Get(config.PlatformTuple()),
		})
	}
}

type driverInfoDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	FilePath string `json:"filePath"`
}

func jsList(args []js.Value) func() (any, error) {
	location := args[0].String()
	return func() (any, error) {
		drivers := config.FindDriverConfigsIn(location)
		out := make([]driverInfoDTO, 0, len(drivers))
		for _, d := range drivers {
			version := ""
			if d.Version != nil {
				version = d.Version.String()
			}
			out = append(out, driverInfoDTO{ID: d.ID, Name: d.Name, Version: version, FilePath: d.FilePath})
		}
		return toJSONValue(out)
	}
}

func jsUninstall(args []js.Value) func() (any, error) {
	name := args[0].String()
	location := args[1].String()
	return func() (any, error) {
		c, err := newClient()
		if err != nil {
			return nil, err
		}
		if err := c.Uninstall(config.Config{Level: config.ConfigEnv, Location: location}, name); err != nil {
			return nil, err
		}
		return "ok", nil
	}
}
