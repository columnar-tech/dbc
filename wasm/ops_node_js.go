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

	"github.com/columnar-tech/dbc/internal/api"
)

func registerNodeOps() {
	js.Global().Set("dbcInstall", promisify(jsInstall))
	js.Global().Set("dbcList", promisify(jsList))
	js.Global().Set("dbcUninstall", promisify(jsUninstall))
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
