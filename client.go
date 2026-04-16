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

package dbc

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/internal"
	"github.com/google/uuid"
	machineid "github.com/zeroshade/machine-id"
)

type clientConfig struct {
	httpClient         *http.Client
	registries         []Registry
	userAgent          string
	baseURL            string
	credentialResolver func(*url.URL) (*auth.Credential, error)
}

type Option func(*clientConfig)

// Client is a driver registry client for searching and managing ADBC drivers.
type Client struct {
	httpClient         *http.Client
	registries         []Registry
	userAgent          string
	mid                string
	uid                uuid.UUID
	setupOnce          sync.Once
	credentialResolver func(*url.URL) (*auth.Credential, error)
}

// NewClient creates a new driver registry client with the given options.
func NewClient(opts ...Option) (*Client, error) {
	cfg := &clientConfig{
		registries: []Registry{
			{BaseURL: mustParseURL("https://dbc-cdn.columnar.tech")},
			{BaseURL: mustParseURL("https://" + auth.DefaultOauthURI())},
		},
		userAgent: fmt.Sprintf("dbc-cli/%s (%s; %s)", Version, runtime.GOOS, runtime.GOARCH),
	}

	for _, opt := range opts {
		opt(cfg)
	}

	httpClient := cfg.httpClient
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: &uaRoundTripper{
				RoundTripper: http.DefaultTransport,
				userAgent:    cfg.userAgent,
			},
		}
	}

	if cfg.baseURL != "" {
		cfg.registries = []Registry{{BaseURL: mustParseURL(cfg.baseURL)}}
	}

	credResolver := cfg.credentialResolver
	if credResolver == nil {
		credResolver = auth.GetCredentials
	}

	return &Client{
		httpClient:         httpClient,
		registries:         cfg.registries,
		userAgent:          cfg.userAgent,
		credentialResolver: credResolver,
	}, nil
}

func (c *Client) setup() {
	c.setupOnce.Do(func() {
		c.mid, _ = machineid.ProtectedID()

		userdir, err := internal.GetUserConfigPath()
		if err != nil {
			c.uid = uuid.New()
			return
		}

		fp := filepath.Join(userdir, "uid.uuid")
		data, err := os.ReadFile(fp)
		if err == nil {
			if err = c.uid.UnmarshalBinary(data); err == nil {
				return
			}
		}

		c.uid = uuid.New()
		if err = os.MkdirAll(filepath.Dir(fp), 0o700); err == nil {
			if data, err = c.uid.MarshalBinary(); err == nil {
				os.WriteFile(fp, data, 0o600)
			}
		}
	})
}

func (c *Client) HTTPClient() *http.Client { return c.httpClient }

// Registries returns the list of driver registries configured for this client.
func (c *Client) Registries() []Registry { return c.registries }

// UserAgent returns the user agent string used by this client.
func (c *Client) UserAgent() string { return c.userAgent }

// WithHTTPClient sets the HTTP client to use for requests.
func WithHTTPClient(hc *http.Client) Option {
	return func(cfg *clientConfig) { cfg.httpClient = hc }
}

// WithRegistries sets the driver registries to use.
func WithRegistries(r []Registry) Option {
	return func(cfg *clientConfig) { cfg.registries = r }
}

// WithBaseURL sets the base URL for the driver registry.
func WithBaseURL(u string) Option {
	return func(cfg *clientConfig) { cfg.baseURL = u }
}

// WithUserAgent sets the user agent string for requests.
func WithUserAgent(ua string) Option {
	return func(cfg *clientConfig) { cfg.userAgent = ua }
}
