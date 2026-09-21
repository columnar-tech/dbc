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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDriverSourceValidation(t *testing.T) {
	tests := []struct {
		name    string
		source  DriverSource
		wantErr string
	}{
		{name: "registry", source: DriverSource{Type: DriverSourceRegistry, URL: "https://registry.example.test"}},
		{name: "packslip", source: DriverSource{Type: DriverSourcePackslip, Project: "owner/project"}},
		{name: "path", source: DriverSource{Type: DriverSourcePath, Path: "../packages/driver.tar.gz"}},
		{name: "empty type", wantErr: "driver source has no type"},
		{name: "unknown type", source: DriverSource{Type: "other", URL: "https://example.test"}, wantErr: `unsupported driver source type "other"`},
		{name: "registry missing url", source: DriverSource{Type: DriverSourceRegistry}, wantErr: "registry source has no URL"},
		{name: "registry with project", source: DriverSource{Type: DriverSourceRegistry, URL: "https://registry.example.test", Project: "owner/project"}, wantErr: "registry source contains fields for another source type"},
		{name: "packslip missing project", source: DriverSource{Type: DriverSourcePackslip}, wantErr: "packslip source has no project"},
		{name: "packslip with url", source: DriverSource{Type: DriverSourcePackslip, Project: "owner/project", URL: "https://registry.example.test"}, wantErr: "packslip source contains fields for another source type"},
		{name: "path missing path", source: DriverSource{Type: DriverSourcePath}, wantErr: "path source has no path"},
		{name: "path with url", source: DriverSource{Type: DriverSourcePath, Path: "package.tar.gz", URL: "https://registry.example.test"}, wantErr: "path source contains fields for another source type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.source.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestDriverSourceJSONRoundTrip(t *testing.T) {
	sources := []DriverSource{
		{Type: DriverSourceRegistry, URL: "https://registry.example.test"},
		{Type: DriverSourcePackslip, Project: "owner/project"},
		{Type: DriverSourcePath, Path: "../packages/driver.tar.gz"},
	}
	for _, source := range sources {
		t.Run(string(source.Type), func(t *testing.T) {
			data, err := json.Marshal(PkgInfo{Source: &source})
			require.NoError(t, err)

			var output PkgInfo
			require.NoError(t, json.Unmarshal(data, &output))
			require.NotNil(t, output.Source)
			assert.Equal(t, source, *output.Source)
		})
	}

	legacy := PkgInfo{}
	legacyData, err := json.Marshal(legacy)
	require.NoError(t, err)
	var legacyRoundTrip PkgInfo
	require.NoError(t, json.Unmarshal(legacyData, &legacyRoundTrip))
	assert.Nil(t, legacyRoundTrip.Source, "omitted legacy source must remain distinguishable from explicit registry")
}
