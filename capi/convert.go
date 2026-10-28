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

//go:build cgo

package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/columnar-tech/dbc/internal/api"
)

// targetFor maps a dbc_target_kind and its directory argument (nil for a C
// NULL) to an api.Target.
func targetFor(kind int32, dir *string) (api.Target, error) {
	switch kind {
	case targetDirectory:
		if dir == nil || *dir == "" {
			return api.Target{}, errors.New("dbc: DBC_TARGET_DIRECTORY requires a directory")
		}
		return api.Target{Location: *dir}, nil
	case targetUser, targetSystem:
		if dir != nil {
			return api.Target{}, errors.New("dbc: directory must be NULL for DBC_TARGET_USER and DBC_TARGET_SYSTEM")
		}
		if kind == targetUser {
			return api.Target{Level: "user"}, nil
		}
		return api.Target{Level: "system"}, nil
	}
	return api.Target{}, fmt.Errorf("dbc: unknown target kind %d", kind)
}

// clientConfig maps dbc_client_new's key/value options (documented in the
// exports.go preamble) onto an api.ClientConfig.
func clientConfig(keys, values []string) (api.ClientConfig, error) {
	var cfg api.ClientConfig
	var cred api.Credential
	type registryOption struct {
		registry api.Registry
		urlSet   bool
		nameSet  bool
	}
	var registries map[int]registryOption
	for i, key := range keys {
		value := values[i]
		switch key {
		case "base_url":
			cfg.BaseURL = value
		case "user_agent_suffix":
			cfg.UserAgentSuffix = value
		case "registry_config":
			cfg.RegistryConfig = value
		case "project_file":
			cfg.ProjectFile = value
		case "credential_store":
			cfg.CredentialStore = value
		case "credential.registry_url":
			cred.RegistryURL = value
		case "credential.auth_uri":
			cred.AuthURI = value
		case "credential.token":
			cred.Token = value
		case "credential.refresh_token":
			cred.RefreshToken = value
		case "credential.client_id":
			cred.ClientID = value
		default:
			suffix, ok := strings.CutPrefix(key, "registry.")
			if !ok {
				return api.ClientConfig{}, fmt.Errorf("dbc: unknown client option %q", key)
			}
			number, field, ok := strings.Cut(suffix, ".")
			index, err := strconv.Atoi(number)
			if !ok || err != nil || index < 0 || strconv.Itoa(index) != number || (field != "url" && field != "name") {
				return api.ClientConfig{}, fmt.Errorf("dbc: invalid registry option %q", key)
			}
			entry := registries[index]
			if field == "url" {
				if entry.urlSet {
					return api.ClientConfig{}, fmt.Errorf("dbc: duplicate client option %q", key)
				}
				entry.registry.URL, entry.urlSet = value, true
			} else {
				if entry.nameSet {
					return api.ClientConfig{}, fmt.Errorf("dbc: duplicate client option %q", key)
				}
				entry.registry.Name, entry.nameSet = value, true
			}
			if registries == nil {
				registries = make(map[int]registryOption)
			}
			registries[index] = entry
		}
	}
	if len(registries) > 0 {
		cfg.Registries = make([]api.Registry, len(registries))
		for i := range cfg.Registries {
			entry, ok := registries[i]
			if !ok || !entry.urlSet || entry.registry.URL == "" {
				return api.ClientConfig{}, fmt.Errorf("dbc: registry.%d.url is required; registry indexes must be contiguous from 0", i)
			}
			cfg.Registries[i] = entry.registry
		}
	}
	if cred != (api.Credential{}) {
		cfg.Credential = &cred
	}
	return cfg, nil
}

// installOptions maps dbc_install_flags onto api.InstallOptions, rejecting
// bits this version doesn't define so callers can't silently get defaults.
func installOptions(flags int32) (api.InstallOptions, error) {
	if unknown := flags &^ installAllowUnsigned; unknown != 0 {
		return api.InstallOptions{}, fmt.Errorf("dbc: unknown install flags %#x", unknown)
	}
	return api.InstallOptions{AllowUnsigned: flags&installAllowUnsigned != 0}, nil
}

// licenseOptions maps dbc_license_flags to the force option.
func licenseOptions(flags int32) (bool, error) {
	if unknown := flags &^ licenseForce; unknown != 0 {
		return false, fmt.Errorf("dbc: unknown license flags %#x", unknown)
	}
	return flags&licenseForce != 0, nil
}
