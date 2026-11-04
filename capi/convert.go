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
	case targetDefault, targetSystem, targetUser:
		if dir != nil {
			return api.Target{}, errors.New("dbc: directory must be NULL unless the target is DBC_TARGET_DIRECTORY")
		}
		switch kind {
		case targetSystem:
			return api.Target{Level: "system"}, nil
		case targetUser:
			return api.Target{Level: "user"}, nil
		}
		return api.Target{}, nil
	}
	return api.Target{}, fmt.Errorf("dbc: unknown target kind %d", kind)
}

// clientConfig maps dbc_client_new's key/value options (documented in the
// exports.go preamble) onto an api.ClientConfig.
func clientConfig(keys, values []string) (api.ClientConfig, error) {
	var cfg api.ClientConfig
	var cred api.Credential
	for i, key := range keys {
		value := values[i]
		switch key {
		case optBaseURL:
			cfg.BaseURL = value
		case optUserAgentSuffix:
			cfg.UserAgentSuffix = value
		case optCredentialRegistryURL:
			cred.RegistryURL = value
		case optCredentialAuthURI:
			cred.AuthURI = value
		case optCredentialToken:
			cred.Token = value
		case optCredentialRefreshToken:
			cred.RefreshToken = value
		case optCredentialClientID:
			cred.ClientID = value
		default:
			return api.ClientConfig{}, fmt.Errorf("dbc: unknown client option %q", key)
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
