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

//go:build js

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"syscall/js"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal"
	"github.com/columnar-tech/dbc/internal/api"
)

func init() {
	// Route auth-internal HTTP (oauth/api-key refresh, license fetch use
	// http.DefaultClient directly) through the JS fetch transport, since Go's
	// default network is disabled under Node js/wasm.
	client := &http.Client{Transport: fetchRoundTripper{}}
	http.DefaultClient = client
	dbc.DefaultClient = client
}

// clients holds one dbc.Client per loadDbc instance, so instances stay
// isolated and each keeps its refreshed credential across calls.
var clients api.Clients

// newFetchClient builds a client from loadDbc's JSON config. An empty string
// uses the host defaults; browser builds never read registry configuration files.
func newFetchClient(cfgJSON string) (*dbc.Client, error) {
	var cfg api.ClientConfig
	if cfgJSON != "" {
		if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
			return nil, fmt.Errorf("invalid client config: %w", err)
		}
	}
	if !filesystemAvailable {
		// Explicit registries take precedence over all file configuration, even
		// unsupported selectors. Otherwise reject an explicit filesystem request
		// instead of silently treating it as the browser's filesystem-free default.
		if cfg.BaseURL == "" && cfg.Registries == nil {
			switch cfg.RegistryConfig {
			case "", "none":
			case "default":
				return nil, fmt.Errorf("registryConfig \"default\" is Node-only")
			default:
				return nil, fmt.Errorf("invalid registryConfig %q: expected \"default\" or \"none\"", cfg.RegistryConfig)
			}
			if cfg.ProjectFile != "" {
				return nil, fmt.Errorf("projectFile is Node-only")
			}
		}
		cfg.RegistryConfig = "none"
		cfg.ProjectFile = ""
		// There is no credential store in the browser, whatever the registries.
		switch cfg.CredentialStore {
		case "", "none":
			cfg.CredentialStore = "none"
		case "default":
			return nil, fmt.Errorf("credentialStore \"default\" is Node-only")
		}
	}
	return api.NewClient(cfg, &http.Client{Transport: fetchRoundTripper{}})
}

func jsNewClient(args []js.Value) func() (any, error) {
	cfgJSON := args[0].String()
	return func() (any, error) {
		c, err := newFetchClient(cfgJSON)
		if err != nil {
			return nil, err
		}
		return int(clients.Add(c)), nil
	}
}

// registerCommon registers the API surface shared by the Node and browser
// builds: configuration hooks plus search/resolve/verify.
func registerCommon() {
	js.Global().Set("dbcSetPlatform", js.FuncOf(func(_ js.Value, a []js.Value) any {
		config.SetPlatformTupleOverride(a[0].String())
		return nil
	}))
	js.Global().Set("dbcNewClient", promisify(jsNewClient))
	js.Global().Set("dbcCloseClient", js.FuncOf(func(_ js.Value, a []js.Value) any {
		clients.Remove(uint64(a[0].Int()))
		return nil
	}))
	js.Global().Set("dbcDebugPaths", promisify(jsDebugPaths))
	js.Global().Set("dbcSearch", promisify(jsSearch))
	js.Global().Set("dbcResolve", promisify(jsResolve))
	js.Global().Set("dbcVerify", promisify(jsVerify))
}

func toJSONValue(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func jsSearch(args []js.Value) func() (any, error) {
	handle := uint64(args[0].Int())
	pattern := ""
	if len(args) > 1 {
		pattern = args[1].String()
	}
	return func() (any, error) {
		c, err := clients.Get(handle)
		if err != nil {
			return nil, err
		}
		res, err := api.Search(context.Background(), c, pattern)
		if err != nil {
			return nil, err
		}
		return toJSONValue(res)
	}
}

func jsResolve(args []js.Value) func() (any, error) {
	handle := uint64(args[0].Int())
	name := args[1].String()
	platform := ""
	if len(args) > 2 && args[2].Type() == js.TypeString {
		platform = args[2].String()
	}
	return func() (any, error) {
		c, err := clients.Get(handle)
		if err != nil {
			return nil, err
		}
		res, err := api.Resolve(context.Background(), c, name, platform)
		if err != nil {
			return nil, err
		}
		return toJSONValue(res)
	}
}

func jsVerify(args []js.Value) func() (any, error) {
	lib := make([]byte, args[0].Get("length").Int())
	js.CopyBytesToGo(lib, args[0])
	sig := make([]byte, args[1].Get("length").Int())
	js.CopyBytesToGo(sig, args[1])
	return func() (any, error) {
		if err := api.Verify(lib, sig); err != nil {
			return nil, err
		}
		return true, nil
	}
}

func jsDebugPaths(_ []js.Value) func() (any, error) {
	return func() (any, error) {
		ucd, ucdErr := internal.UserConfigDir()
		home, homeErr := os.UserHomeDir()
		return toJSONValue(map[string]any{
			"userConfigDir":    ucd,
			"userConfigDirErr": errString(ucdErr),
			"userHomeDir":      home,
			"userHomeDirErr":   errString(homeErr),
			"platformTuple":    config.PlatformTuple(),
		})
	}
}
