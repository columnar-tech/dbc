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
	"testing"

	"github.com/columnar-tech/dbc/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetFor(t *testing.T) {
	dir, empty := "/opt/drivers", ""
	for _, tc := range []struct {
		name string
		kind int32
		dir  *string
		want api.Target
		err  string
	}{
		{"directory", targetDirectory, &dir, api.Target{Location: dir}, ""},
		{"user", targetUser, nil, api.Target{Level: "user"}, ""},
		{"system", targetSystem, nil, api.Target{Level: "system"}, ""},
		{"directory without path", targetDirectory, nil, api.Target{}, "dbc: DBC_TARGET_DIRECTORY requires a directory"},
		{"directory with empty path", targetDirectory, &empty, api.Target{}, "dbc: DBC_TARGET_DIRECTORY requires a directory"},
		{"level with path", targetUser, &dir, api.Target{}, "dbc: directory must be NULL for DBC_TARGET_USER and DBC_TARGET_SYSTEM"},
		{"unknown kind", 7, nil, api.Target{}, "dbc: unknown target kind 7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := targetFor(tc.kind, tc.dir)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The option keys are the C API's configuration contract; a typo here
// compiles fine and silently changes what callers can configure.
func TestClientConfig(t *testing.T) {
	t.Run("every documented key", func(t *testing.T) {
		cfg, err := clientConfig(
			[]string{"base_url", "user_agent_suffix", "credential.registry_url", "credential.auth_uri",
				"credential.token", "credential.refresh_token", "credential.client_id"},
			[]string{"https://registry.test", "app/1", "https://private.test", "https://auth.test", "t", "rt", "cid"},
		)
		require.NoError(t, err)
		assert.Equal(t, api.ClientConfig{
			BaseURL:         "https://registry.test",
			UserAgentSuffix: "app/1",
			Credential: &api.Credential{
				RegistryURL: "https://private.test", AuthURI: "https://auth.test",
				Token: "t", RefreshToken: "rt", ClientID: "cid",
			},
		}, cfg)
	})

	t.Run("no credential keys means no credential", func(t *testing.T) {
		cfg, err := clientConfig([]string{"base_url"}, []string{"https://registry.test"})
		require.NoError(t, err)
		assert.Nil(t, cfg.Credential)
	})

	t.Run("unknown key", func(t *testing.T) {
		_, err := clientConfig([]string{"base-url"}, []string{"x"})
		require.EqualError(t, err, `dbc: unknown client option "base-url"`)
	})

	t.Run("registry configuration keys", func(t *testing.T) {
		cfg, err := clientConfig(
			[]string{"registry.1.name", "registry.0.url", "registry.1.url", "registry_config", "project_file", "credential_store"},
			[]string{"Private", "https://first.test", "https://second.test", "none", "/tmp/dbc.toml", "none"},
		)
		require.NoError(t, err)
		assert.Equal(t, api.ClientConfig{
			Registries: []api.Registry{
				{URL: "https://first.test"},
				{URL: "https://second.test", Name: "Private"},
			},
			RegistryConfig:  "none",
			ProjectFile:     "/tmp/dbc.toml",
			CredentialStore: "none",
		}, cfg)
	})

	t.Run("empty registry url", func(t *testing.T) {
		_, err := clientConfig([]string{"registry.0.url"}, []string{""})
		require.ErrorContains(t, err, "registry.0.url is required")
	})

	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"duplicate url", []string{"registry.0.url", "registry.0.url"}},
		{"duplicate name", []string{"registry.0.url", "registry.0.name", "registry.0.name"}},
		{"gap", []string{"registry.0.url", "registry.2.url"}},
		{"starts at one", []string{"registry.1.url"}},
		{"name without url", []string{"registry.0.name"}},
		{"negative index", []string{"registry.-1.url"}},
		{"non numeric index", []string{"registry.x.url"}},
		{"leading zero", []string{"registry.00.url"}},
		{"unknown registry field", []string{"registry.0.title"}},
		{"oversized index", []string{"registry.999999999999999999999999999.url"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := make([]string, len(tc.keys))
			for i := range values {
				values[i] = "https://registry.test"
			}
			_, err := clientConfig(tc.keys, values)
			require.Error(t, err)
		})
	}
}

func TestInstallOptions(t *testing.T) {
	opts, err := installOptions(0)
	require.NoError(t, err)
	assert.False(t, opts.AllowUnsigned, "signatures are verified by default")

	opts, err = installOptions(installAllowUnsigned)
	require.NoError(t, err)
	assert.True(t, opts.AllowUnsigned)

	_, err = installOptions(installAllowUnsigned | 0x4)
	require.EqualError(t, err, "dbc: unknown install flags 0x4")
}

func TestLicenseOptions(t *testing.T) {
	force, err := licenseOptions(0)
	require.NoError(t, err)
	assert.False(t, force, "existing licenses are preserved by default")

	force, err = licenseOptions(licenseForce)
	require.NoError(t, err)
	assert.True(t, force)

	_, err = licenseOptions(licenseForce | 0x4)
	require.EqualError(t, err, "dbc: unknown license flags 0x4")
}
