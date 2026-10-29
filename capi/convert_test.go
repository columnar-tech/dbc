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
