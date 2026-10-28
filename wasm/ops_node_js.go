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
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/columnar-tech/dbc/internal/api"
)

func registerNodeOps() {
	js.Global().Set("dbcInstall", promisify(jsInstall))
	js.Global().Set("dbcList", promisify(jsList))
	js.Global().Set("dbcUninstall", promisify(jsUninstall))
	js.Global().Set("dbcLogin", promisify(jsLogin))
	js.Global().Set("dbcLogout", promisify(jsLogout))
	js.Global().Set("dbcLicensePath", promisify(jsLicensePath))
	js.Global().Set("dbcInstallLicense", promisify(jsInstallLicense))
}

// The npm API takes a directory only; config levels are not exposed to JS.
func jsInstall(args []js.Value) func() (any, error) {
	handle := uint64(args[0].Int())
	name := args[1].String()
	target := api.Target{Location: args[2].String()}
	opts := api.InstallOptions{AllowUnsigned: len(args) > 3 && args[3].Truthy()}
	return func() (any, error) {
		c, err := clients.Get(handle)
		if err != nil {
			return nil, err
		}
		m, err := api.Install(context.Background(), c, name, target, opts)
		if err != nil {
			return nil, err
		}
		return toJSONValue(m)
	}
}

func jsList(args []js.Value) func() (any, error) {
	target := api.Target{Location: args[0].String()}
	return func() (any, error) {
		drivers, err := api.ListInstalled(target)
		if err != nil {
			return nil, err
		}
		return toJSONValue(drivers)
	}
}

func jsUninstall(args []js.Value) func() (any, error) {
	name := args[0].String()
	target := api.Target{Location: args[1].String()}
	return func() (any, error) {
		if err := api.Uninstall(name, target); err != nil {
			return nil, err
		}
		return "ok", nil
	}
}

func jsLogin(args []js.Value) func() (any, error) {
	credJSON := args[0].String()
	return func() (any, error) {
		var cred api.Credential
		if err := json.Unmarshal([]byte(credJSON), &cred); err != nil {
			return nil, fmt.Errorf("invalid credential: %w", err)
		}
		if err := api.Login(cred); err != nil {
			return nil, err
		}
		return "ok", nil
	}
}

func jsLogout(args []js.Value) func() (any, error) {
	registryURL := args[0].String()
	return func() (any, error) {
		if err := api.Logout(registryURL); err != nil {
			return nil, err
		}
		return "ok", nil
	}
}

func jsLicensePath(_ []js.Value) func() (any, error) {
	return func() (any, error) {
		return api.LicensePath()
	}
}

func jsInstallLicense(args []js.Value) func() (any, error) {
	srcPath := args[0].String()
	force := len(args) > 1 && args[1].Truthy()
	return func() (any, error) {
		if err := api.InstallLicense(srcPath, force); err != nil {
			return nil, err
		}
		return "ok", nil
	}
}
