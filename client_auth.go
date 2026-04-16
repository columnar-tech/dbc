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
	"net/url"

	"github.com/columnar-tech/dbc/auth"
)

// WithCredential sets a specific credential to use for all requests.
func WithCredential(cred *auth.Credential) Option {
	return func(cfg *clientConfig) {
		cfg.credentialResolver = func(_ *url.URL) (*auth.Credential, error) {
			return cred, nil
		}
	}
}

// WithAuthFromFilesystem configures the client to read credentials from the filesystem.
func WithAuthFromFilesystem() Option {
	return func(cfg *clientConfig) {
		cfg.credentialResolver = auth.GetCredentials
	}
}

func (c *Client) getCredential(u *url.URL) (*auth.Credential, error) {
	if c.credentialResolver == nil {
		return nil, nil
	}
	return c.credentialResolver(u)
}

// Login saves a credential for the given registry.
func (c *Client) Login(cred *auth.Credential) error {
	return auth.AddCredential(*cred, true)
}

// Logout removes the credential for the given registry URL.
func (c *Client) Logout(registryURL *url.URL) error {
	return auth.RemoveCredential(auth.Uri(*registryURL))
}
