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

import "github.com/columnar-tech/dbc/config"

// GetConfig returns the configuration at the specified level.
func (c *Client) GetConfig(level config.ConfigLevel) config.Config {
	return config.Get()[level]
}

// ListInstalled returns a list of installed drivers at the specified configuration level.
func (c *Client) ListInstalled(level config.ConfigLevel) []config.DriverInfo {
	return config.FindDriverConfigs(level)
}

// GetDriver retrieves driver information from the given configuration.
func (c *Client) GetDriver(cfg config.Config, name string) (config.DriverInfo, error) {
	return config.GetDriver(cfg, name)
}

// CreateManifest creates a manifest file for the given driver configuration.
func (c *Client) CreateManifest(cfg config.Config, di config.DriverInfo) error {
	return config.CreateManifest(cfg, di)
}
