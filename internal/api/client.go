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
)

// Credential is a token credential for one private registry, injected by the
// host instead of being read from dbc's credential store.
type Credential struct {
	RegistryURL  string `json:"registryURL"`
	AuthURI      string `json:"authURI"`
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientID"`
}

// ClientConfig configures a client. The zero value targets Columnar's public
// registry with dbc's default credential lookup.
type ClientConfig struct {
	BaseURL         string      `json:"baseURL"`
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
	if cfg.BaseURL != "" {
		opts = append(opts, dbc.WithBaseURL(cfg.BaseURL))
	}
	if cfg.UserAgentSuffix != "" {
		opts = append(opts, dbc.WithUserAgentSuffix(cfg.UserAgentSuffix))
	}
	if cfg.Credential != nil {
		opt, err := credentialOption(*cfg.Credential)
		if err != nil {
			return nil, err
		}
		opts = append(opts, opt)
	}
	return dbc.NewClient(opts...)
}

// credentialOption serves c for its registry host only. The *auth.Credential
// is shared by every request on the client, so a refreshed access token is
// kept instead of being rebuilt from the original (possibly stale) token.
func credentialOption(c Credential) (dbc.Option, error) {
	regURL, err := url.Parse(c.RegistryURL)
	if err != nil {
		return nil, fmt.Errorf("invalid credential registryURL: %w", err)
	}
	authURI, err := url.Parse(c.AuthURI)
	if err != nil {
		return nil, fmt.Errorf("invalid credential authURI: %w", err)
	}
	cred := &auth.Credential{
		Type:         auth.TypeToken,
		AuthURI:      auth.Uri(*authURI),
		RegistryURL:  auth.Uri(*regURL),
		Token:        c.Token,
		RefreshToken: c.RefreshToken,
		ClientID:     c.ClientID,
	}
	host := regURL.Host
	return dbc.WithCredentialResolver(func(u *url.URL) (*auth.Credential, error) {
		if u.Host == host {
			return cred, nil
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
