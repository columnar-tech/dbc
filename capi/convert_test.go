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
		{"default", targetDefault, nil, api.Target{}, ""},
		{"directory", targetDirectory, &dir, api.Target{Location: dir}, ""},
		{"user", targetUser, nil, api.Target{Level: "user"}, ""},
		{"system", targetSystem, nil, api.Target{Level: "system"}, ""},
		{"directory without path", targetDirectory, nil, api.Target{}, "dbc: DBC_TARGET_DIRECTORY requires a directory"},
		{"directory with empty path", targetDirectory, &empty, api.Target{}, "dbc: DBC_TARGET_DIRECTORY requires a directory"},
		{"level with path", targetUser, &dir, api.Target{}, "dbc: directory must be NULL unless the target is DBC_TARGET_DIRECTORY"},
		{"default with path", targetDefault, &dir, api.Target{}, "dbc: directory must be NULL unless the target is DBC_TARGET_DIRECTORY"},
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

func TestClientConfigRegistryCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		vals []string
		want []api.Registry
	}{
		{"omitted", nil, nil, nil},
		{"zero", []string{"registry.count"}, []string{"0"}, []api.Registry{}},
		{
			"two entries", []string{"registry.count", "registry.1.name", "registry.0.url", "registry.1.url"},
			[]string{"2", "Second", "https://first.test", "https://second.test"},
			[]api.Registry{{URL: "https://first.test"}, {URL: "https://second.test", Name: "Second"}},
		},
		{
			"count after entries", []string{"registry.0.url", "registry.count"},
			[]string{"https://first.test", "1"}, []api.Registry{{URL: "https://first.test"}},
		},
		{"leading zeros are decimal", []string{"registry.count"}, []string{"00"}, []api.Registry{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := clientConfig(tc.keys, tc.vals)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Registries)
		})
	}

	for _, tc := range []struct {
		name string
		keys []string
		vals []string
	}{
		{"too few entries", []string{"registry.count", "registry.0.url"}, []string{"2", "https://first.test"}},
		{"too many entries", []string{"registry.count", "registry.0.url", "registry.1.url"}, []string{"1", "https://first.test", "https://second.test"}},
		{"zero with an entry", []string{"registry.count", "registry.0.url"}, []string{"0", "https://first.test"}},
		{"index outside count", []string{"registry.count", "registry.1.url"}, []string{"1", "https://first.test"}},
		{"missing url", []string{"registry.count", "registry.0.name"}, []string{"1", "First"}},
		{"empty url", []string{"registry.count", "registry.0.url"}, []string{"1", ""}},
		{"large count without entries", []string{"registry.count"}, []string{"2147483647"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := clientConfig(tc.keys, tc.vals)
			require.Error(t, err)
		})
	}

	for _, value := range []string{"", "-1", "-0", "+1", " 1", "1 ", "1.0", "1e2", "0x1", "1_0", "one", "999999999999999999999999999"} {
		t.Run("invalid count "+value, func(t *testing.T) {
			_, err := clientConfig([]string{"registry.count"}, []string{value})
			require.ErrorContains(t, err, "registry.count")
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
