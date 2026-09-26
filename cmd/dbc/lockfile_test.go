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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadLockFileVersion(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		wantErr  string
	}{
		{
			name:     "version 1",
			contents: "version = 1\n[[drivers]]\nname = 'test-driver-1'\nversion = '1.0.0'\n",
		},
		{
			name:     "version 2",
			contents: "version = 2\n",
			wantErr:  "unsupported lock file version 2",
		},
		{
			name: "version 2 with driver metadata",
			contents: "version = 2\nrevision = 0\n" +
				"[[drivers]]\nname = 'test-driver-1'\nversion = '1.0.0'\n" +
				"[drivers.source]\ntype = 'packslip'\n" +
				"[[drivers.artifacts]]\nformat = 'tar.gz'\n",
			wantErr: "unsupported lock file version 2",
		},
		{
			name:     "future version",
			contents: "version = 99\n",
			wantErr:  "unsupported lock file version 99",
		},
		{
			name:     "missing version",
			contents: "[[drivers]]\nname = 'test-driver-1'\nversion = '1.0.0'\n",
			wantErr:  "unsupported lock file version 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dbc.lock")
			require.NoError(t, os.WriteFile(path, []byte(tt.contents), 0o644))

			lock, err := loadLockFile(path)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, lock.lockinfo)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, lockFileVersion, lock.Version)
			assert.Equal(t, "1.0.0", lock.lockinfo["test-driver-1"].Version.String())
		})
	}
}
