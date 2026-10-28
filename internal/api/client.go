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

// Package api is the transport-agnostic operation layer behind the js/wasm
// bindings (wasm/) and the C ABI (capi/). The JSON field names on its types
// are the wire contract for @columnar-tech/dbc-wasm and the columnar-dbc Rust
// crates; renaming one is a breaking change for both.
package api

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/internal"
)

// Credential is a token credential for one private registry. An injected
// credential replaces stored credentials for its host, not for other hosts.
type Credential struct {
	RegistryURL  string `json:"registryURL"`
	AuthURI      string `json:"authURI"`
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientID"`
}

// Registry identifies a driver registry in an explicit, ordered list.
type Registry struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
}

// ClientConfig configures a client. The zero value loads the user's global
// config.toml, merges it with built-in registries, and uses stored credentials.
type ClientConfig struct {
	BaseURL string `json:"baseURL"`
	// Registries is used verbatim when non-nil, including an empty slice.
	// It conflicts with BaseURL; either bypasses RegistryConfig and ProjectFile.
	Registries []Registry `json:"registries"`
	// RegistryConfig is ""/"default" to load config.toml, or "none" to skip it.
	RegistryConfig string `json:"registryConfig"`
	// ProjectFile explicitly names a dbc.toml; a missing file is an error.
	ProjectFile string `json:"projectFile"`
	// CredentialStore is ""/"default" to allow stored credential lookup, or
	// "none" to disable it. Injected credentials can still refresh and persist.
	CredentialStore string      `json:"credentialStore"`
	Credential      *Credential `json:"credential"`
	UserAgentSuffix string      `json:"userAgentSuffix"`
}

// NewClient builds a dbc.Client from cfg. hc replaces the HTTP client (the
// js/wasm build passes a fetch-backed one); nil keeps dbc's default.
func NewClient(cfg ClientConfig, hc *http.Client) (*dbc.Client, error) {
	var opts []dbc.Option
	if hc != nil {
		opts = append(opts, dbc.WithHTTPClient(hc))
	}
	if cfg.BaseURL != "" && cfg.Registries != nil {
		return nil, fmt.Errorf("set either baseURL or registries, not both")
	}
	switch {
	case cfg.BaseURL != "":
		// dbc.WithBaseURL panics on a malformed URL; report it as an error.
		if _, err := url.Parse(cfg.BaseURL); err != nil {
			return nil, fmt.Errorf("invalid baseURL: %w", err)
		}
		opts = append(opts, dbc.WithBaseURL(cfg.BaseURL))
	case cfg.Registries != nil:
		registries := make([]dbc.Registry, len(cfg.Registries))
		for i, r := range cfg.Registries {
			u, err := url.Parse(r.URL)
			if err != nil {
				return nil, fmt.Errorf("invalid registries[%d].url: %w", i, err)
			}
			registries[i] = dbc.Registry{BaseURL: u, Name: r.Name}
		}
		opts = append(opts, dbc.WithRegistries(registries))
	default:
		switch cfg.RegistryConfig {
		case "", "default":
			dir, err := internal.GetUserConfigPath()
			if err != nil {
				return nil, fmt.Errorf("failed to locate registry config directory: %w", err)
			}
			global, err := dbc.LoadGlobalConfig(dir)
			if err != nil {
				return nil, fmt.Errorf("failed to load registry config: %w", err)
			}
			opts = append(opts, dbc.WithGlobalConfig(global))
		case "none":
		default:
			return nil, fmt.Errorf("invalid registryConfig %q: expected default or none", cfg.RegistryConfig)
		}
		if cfg.ProjectFile != "" {
			registries, replaceDefaults, err := dbc.LoadProjectRegistries(cfg.ProjectFile)
			if err != nil {
				return nil, err
			}
			opts = append(opts, dbc.WithProjectRegistries(registries, replaceDefaults))
		}
	}
	if cfg.UserAgentSuffix != "" {
		opts = append(opts, dbc.WithUserAgentSuffix(cfg.UserAgentSuffix))
	}
	switch cfg.CredentialStore {
	case "", "default", "none":
	default:
		return nil, fmt.Errorf("invalid credentialStore %q: expected default or none", cfg.CredentialStore)
	}
	if cfg.Credential != nil {
		opt, err := credentialOption(*cfg.Credential, cfg.CredentialStore != "none")
		if err != nil {
			return nil, err
		}
		opts = append(opts, opt)
	} else if cfg.CredentialStore == "none" {
		opts = append(opts, dbc.WithCredentialResolver(func(*url.URL) (*auth.Credential, error) {
			return nil, nil
		}))
	}
	return dbc.NewClient(opts...)
}

// tokenCredential validates the wire URLs and builds a stored or injected
// token credential with the same fields in either case.
func tokenCredential(c Credential) (auth.Credential, error) {
	regURL, err := url.Parse(c.RegistryURL)
	if err != nil {
		return auth.Credential{}, fmt.Errorf("invalid credential registryURL: %w", err)
	}
	authURI, err := url.Parse(c.AuthURI)
	if err != nil {
		return auth.Credential{}, fmt.Errorf("invalid credential authURI: %w", err)
	}
	return auth.Credential{
		Type:         auth.TypeToken,
		AuthURI:      auth.Uri(*authURI),
		RegistryURL:  auth.Uri(*regURL),
		Token:        c.Token,
		RefreshToken: c.RefreshToken,
		ClientID:     c.ClientID,
	}, nil
}

// credentialOption serves c for its registry host, optionally falling back to
// the store for other hosts. The *auth.Credential is shared by every request
// so a refreshed access token is kept instead of rebuilt from a stale token.
func credentialOption(c Credential, useStore bool) (dbc.Option, error) {
	cred, err := tokenCredential(c)
	if err != nil {
		return nil, err
	}
	host := cred.RegistryURL.Host
	return dbc.WithCredentialResolver(func(u *url.URL) (*auth.Credential, error) {
		if u.Host == host {
			return &cred, nil
		}
		if useStore {
			return auth.GetCredentials(u)
		}
		return nil, nil
	}), nil
}

// Clients maps opaque handles to clients for hosts that cannot hold Go
// pointers (JS, C). Handles start at 1 and are never reused. The zero value
// is ready to use.
type Clients struct {
	mu   sync.Mutex
	m    map[uint64]*dbc.Client
	next uint64
}

func (r *Clients) Add(c *dbc.Client) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[uint64]*dbc.Client{}
	}
	r.next++
	r.m[r.next] = c
	return r.next
}

func (r *Clients) Get(h uint64) (*dbc.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.m[h]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("invalid client handle %d", h)
}

func (r *Clients) Remove(h uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, h)
}
