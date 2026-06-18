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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"syscall/js"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/config"
)

var (
	baseURL      string
	credResolver func(*url.URL) (*auth.Credential, error)
)

func init() {
	// Route auth-internal HTTP (oauth/api-key refresh, license fetch use
	// http.DefaultClient directly) through the JS fetch transport, since Go's
	// default network is disabled under Node js/wasm.
	client := &http.Client{Transport: fetchRoundTripper{}}
	http.DefaultClient = client
	dbc.DefaultClient = client
}

func newClient() (*dbc.Client, error) {
	opts := []dbc.Option{dbc.WithHTTPClient(&http.Client{Transport: fetchRoundTripper{}})}
	if baseURL != "" {
		opts = append(opts, dbc.WithBaseURL(baseURL))
	}
	if credResolver != nil {
		opts = append(opts, dbc.WithCredentialResolver(credResolver))
	}
	return dbc.NewClient(opts...)
}

func main() {
	js.Global().Set("dbcSetBaseURL", js.FuncOf(func(_ js.Value, a []js.Value) any {
		baseURL = a[0].String()
		return nil
	}))
	js.Global().Set("dbcSetPlatform", js.FuncOf(func(_ js.Value, a []js.Value) any {
		config.SetPlatformTupleOverride(a[0].String())
		return nil
	}))
	js.Global().Set("dbcSetOAuthCredential", js.FuncOf(func(_ js.Value, a []js.Value) any {
		regURL, _ := url.Parse(a[0].String())
		authURI, _ := url.Parse(a[1].String())
		cred := &auth.Credential{
			Type:         auth.TypeToken,
			AuthURI:      auth.Uri(*authURI),
			RegistryURL:  auth.Uri(*regURL),
			Token:        a[2].String(),
			RefreshToken: a[3].String(),
			ClientID:     a[4].String(),
		}
		credResolver = func(u *url.URL) (*auth.Credential, error) {
			if u.Host == regURL.Host {
				return cred, nil
			}
			return nil, nil
		}
		return nil
	}))
	js.Global().Set("dbcDebugPaths", promisify(jsDebugPaths))
	js.Global().Set("dbcSearch", promisify(jsSearch))
	js.Global().Set("dbcInstall", promisify(jsInstall))
	js.Global().Set("dbcList", promisify(jsList))
	js.Global().Set("dbcUninstall", promisify(jsUninstall))
	js.Global().Set("dbcVerify", promisify(jsVerify))
	js.Global().Get("console").Call("log", "dbc-wasm ready")
	select {}
}

func toJSONValue(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

type driverDTO struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	License     string `json:"license"`
	Description string `json:"description"`
}

func jsSearch(args []js.Value) func() (any, error) {
	pattern := ""
	if len(args) > 0 {
		pattern = args[0].String()
	}
	return func() (any, error) {
		c, err := newClient()
		if err != nil {
			return nil, err
		}
		drivers, err := c.Search(context.Background(), pattern)
		if err != nil {
			return nil, err
		}
		out := make([]driverDTO, 0, len(drivers))
		for _, d := range drivers {
			out = append(out, driverDTO{Path: d.Path, Title: d.Title, License: d.License, Description: d.Desc})
		}
		return toJSONValue(out)
	}
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
		cfg := config.Config{Level: config.ConfigEnv, Location: location}
		m, err := c.Install(context.Background(), cfg, name)
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
		os.Setenv("ADBC_DRIVER_PATH", location)
		drivers := config.FindDriverConfigs(config.ConfigEnv)
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
		cfg := config.Config{Level: config.ConfigEnv, Location: location}
		if err := c.Uninstall(cfg, name); err != nil {
			return nil, err
		}
		return "ok", nil
	}
}

func jsVerify(args []js.Value) func() (any, error) {
	lib := make([]byte, args[0].Get("length").Int())
	js.CopyBytesToGo(lib, args[0])
	sig := make([]byte, args[1].Get("length").Int())
	js.CopyBytesToGo(sig, args[1])
	return func() (any, error) {
		if err := dbc.SignedByColumnar(bytes.NewReader(lib), bytes.NewReader(sig)); err != nil {
			return nil, err
		}
		return true, nil
	}
}

func jsDebugPaths(_ []js.Value) func() (any, error) {
	return func() (any, error) {
		ucd, ucdErr := os.UserConfigDir()
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

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
