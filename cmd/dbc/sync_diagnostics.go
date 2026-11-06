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

package main

import (
	"net/url"

	"github.com/columnar-tech/dbc"
)

// redactRegistryError applies value-based redaction only at boundaries that
// handle registry credentials. Local integrity and lockfile errors need only
// URL stripping, so a short configured value cannot hide their diagnostics.
func redactRegistryError(err error, configured func() ([]dbc.Registry, error), extra ...*url.URL) error {
	if err == nil {
		return nil
	}
	var registries []dbc.Registry
	if configured != nil {
		registries, _ = configured()
	}
	for _, u := range extra {
		if u != nil {
			registries = append(registries, dbc.Registry{BaseURL: u})
		}
	}
	return redactURLSecrets(err, registrySecrets(registries))
}
