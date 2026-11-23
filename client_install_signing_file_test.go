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

//go:build !js || dbcnode

package dbc_test

import (
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The browser build rejects public_key_file, so this runs elsewhere.
func TestClientInstallKeyFileFailsBeforeDownload(t *testing.T) {
	archive, _ := signedTestArchive(t, "acme")

	var downloads atomic.Int32
	u := signedDriverServer(t, archive, true, &downloads)
	dir := t.TempDir()
	path := filepath.Join(dir, "dbc.toml")
	require.NoError(t, os.WriteFile(path, []byte(
		"exclude_builtin_registries = true\n[signing_keys.acme]\ntype = 'openpgp'\npublic_key_file = 'missing.asc'\n"+
			"[[registries]]\nurl = '"+u.String()+"'\nsigning_key = 'acme'\n"), 0o600))
	project, err := dbc.LoadProjectConfig(path)
	require.NoError(t, err)
	c, err := dbc.NewClient(dbc.WithHTTPClient(&http.Client{}), dbc.WithProjectConfig(project))
	require.NoError(t, err)
	cfg := config.Config{Level: config.ConfigEnv, Location: t.TempDir()}
	_, err = c.Install(t.Context(), cfg, "signed-driver")
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.ErrorContains(t, err, "could not load the signing key")
	assert.Zero(t, downloads.Load())

	// AllowUnsigned does not need the key.
	_, err = c.Install(t.Context(), cfg, "signed-driver", dbc.AllowUnsigned())
	require.NoError(t, err)
}
