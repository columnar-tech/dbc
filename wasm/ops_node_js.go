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
	"errors"
	"syscall/js"

	"github.com/columnar-tech/dbc/internal/api"
)

func registerNodeOps() {
	js.Global().Set("dbcInstall", promisify(jsInstall))
	js.Global().Set("dbcList", promisify(jsList))
	js.Global().Set("dbcUninstall", promisify(jsUninstall))
}

// The npm API takes a directory only; config levels are not exposed to JS.
// An empty api.Target means the CLI's default location, so a missing
// location must be an error here, as it always was for the npm package.
var errLocationRequired = errors.New("location is required: the npm API installs to an explicit directory")

// locationTarget is the target for a JS location argument, which must be a
// non-empty string; js.Value.String() would turn undefined into a relative
// directory name.
func locationTarget(v js.Value) (api.Target, error) {
	if v.Type() != js.TypeString || v.String() == "" {
		return api.Target{}, errLocationRequired
	}
	return api.Target{Location: v.String()}, nil
}

func jsInstall(args []js.Value) func() (any, error) {
	handle := uint64(args[0].Int())
	name := args[1].String()
	target, targetErr := locationTarget(args[2])
	opts := api.InstallOptions{AllowUnsigned: len(args) > 3 && args[3].Truthy()}
	return func() (any, error) {
		if targetErr != nil {
			return nil, targetErr
		}
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
	target, targetErr := locationTarget(args[0])
	return func() (any, error) {
		if targetErr != nil {
			return nil, targetErr
		}
		drivers, err := api.ListInstalled(target)
		if err != nil {
			return nil, err
		}
		return toJSONValue(drivers)
	}
}

func jsUninstall(args []js.Value) func() (any, error) {
	name := args[0].String()
	target, targetErr := locationTarget(args[1])
	return func() (any, error) {
		if targetErr != nil {
			return nil, targetErr
		}
		if err := api.Uninstall(name, target); err != nil {
			return nil, err
		}
		return "ok", nil
	}
}
