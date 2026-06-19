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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"syscall/js"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/config"
)

type clientCredentialJSON struct {
	RegistryURL  string `json:"registryURL"`
	AuthURI      string `json:"authURI"`
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientID"`
}

type clientConfigJSON struct {
	BaseURL    string                `json:"baseURL"`
	Credential *clientCredentialJSON `json:"credential"`
}

func init() {
	// Route auth-internal HTTP (oauth/api-key refresh, license fetch use
	// http.DefaultClient directly) through the JS fetch transport, since Go's
	// default network is disabled under Node js/wasm.
	client := &http.Client{Transport: fetchRoundTripper{}}
	http.DefaultClient = client
	dbc.DefaultClient = client
}

// clientFromConfig builds a per-call dbc.Client from a JSON config string
// ({baseURL, credential}). Config is passed on each network call rather than
// stored in a shared global so multiple clients in one process cannot clobber
// each other's registry/credentials. An empty string yields a bare client.
func clientFromConfig(cfgJSON string) (*dbc.Client, error) {
	opts := []dbc.Option{dbc.WithHTTPClient(&http.Client{Transport: fetchRoundTripper{}})}
	if cfgJSON == "" {
		return dbc.NewClient(opts...)
	}
	var cc clientConfigJSON
	if err := json.Unmarshal([]byte(cfgJSON), &cc); err != nil {
		return nil, fmt.Errorf("invalid client config: %w", err)
	}
	if cc.BaseURL != "" {
		opts = append(opts, dbc.WithBaseURL(cc.BaseURL))
	}
	if cc.Credential != nil {
		regURL, err := url.Parse(cc.Credential.RegistryURL)
		if err != nil {
			return nil, fmt.Errorf("invalid credential registryURL: %w", err)
		}
		authURI, err := url.Parse(cc.Credential.AuthURI)
		if err != nil {
			return nil, fmt.Errorf("invalid credential authURI: %w", err)
		}
		cred := &auth.Credential{
			Type:         auth.TypeToken,
			AuthURI:      auth.Uri(*authURI),
			RegistryURL:  auth.Uri(*regURL),
			Token:        cc.Credential.Token,
			RefreshToken: cc.Credential.RefreshToken,
			ClientID:     cc.Credential.ClientID,
		}
		host := regURL.Host
		opts = append(opts, dbc.WithCredentialResolver(func(u *url.URL) (*auth.Credential, error) {
			if u.Host == host {
				return cred, nil
			}
			return nil, nil
		}))
	}
	return dbc.NewClient(opts...)
}

// registerCommon registers the API surface shared by the Node and browser
// builds: configuration hooks plus search/resolve/verify.
func registerCommon() {
	js.Global().Set("dbcSetPlatform", js.FuncOf(func(_ js.Value, a []js.Value) any {
		config.SetPlatformTupleOverride(a[0].String())
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

type driverDTO struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	License     string `json:"license"`
	Description string `json:"description"`
}

type searchResultDTO struct {
	Drivers []driverDTO `json:"drivers"`
	Warning string      `json:"warning,omitempty"`
}

func jsSearch(args []js.Value) func() (any, error) {
	cfgJSON := args[0].String()
	pattern := ""
	if len(args) > 1 {
		pattern = args[1].String()
	}
	return func() (any, error) {
		c, err := clientFromConfig(cfgJSON)
		if err != nil {
			return nil, err
		}
		drivers, searchErr := c.Search(context.Background(), pattern)
		if searchErr != nil && len(drivers) == 0 {
			return nil, searchErr
		}
		out := searchResultDTO{Drivers: make([]driverDTO, 0, len(drivers))}
		for _, d := range drivers {
			out.Drivers = append(out.Drivers, driverDTO{Path: d.Path, Title: d.Title, License: d.License, Description: d.Desc})
		}
		if searchErr != nil {
			out.Warning = searchErr.Error()
		}
		return toJSONValue(out)
	}
}

type resolveDTO struct {
	Path     string   `json:"path"`
	Platform string   `json:"platform"`
	Versions []string `json:"versions"`
	Latest   *struct {
		Version string `json:"version"`
		URL     string `json:"url"`
	} `json:"latest,omitempty"`
}

func jsResolve(args []js.Value) func() (any, error) {
	cfgJSON := args[0].String()
	name := args[1].String()
	platform := ""
	if len(args) > 2 && args[2].Type() == js.TypeString {
		platform = args[2].String()
	}
	return func() (any, error) {
		if platform == "" {
			platform = config.PlatformTuple()
		}
		c, err := clientFromConfig(cfgJSON)
		if err != nil {
			return nil, err
		}
		drivers, searchErr := c.Search(context.Background(), name)
		if searchErr != nil && len(drivers) == 0 {
			return nil, searchErr
		}
		for _, d := range drivers {
			if d.Path != name {
				continue
			}
			versions := []string{}
			for _, v := range d.Versions(platform) {
				versions = append(versions, v.String())
			}
			dto := resolveDTO{Path: d.Path, Platform: platform, Versions: versions}
			if pkg, perr := d.GetPackage(nil, platform, false); perr == nil && pkg.Path != nil {
				dto.Latest = &struct {
					Version string `json:"version"`
					URL     string `json:"url"`
				}{Version: pkg.Version.String(), URL: pkg.Path.String()}
			}
			return toJSONValue(dto)
		}
		if searchErr != nil {
			return nil, fmt.Errorf("driver %q not found in reachable registries; some registries failed: %w", name, searchErr)
		}
		return nil, fmt.Errorf("driver %q not found", name)
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
