//go:build !windows && !js

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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInflateTarballDoesNotInheritArchiveMode(t *testing.T) {
	f := writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("driver.so", "x"))
	out := t.TempDir()
	_, err := InflateTarball(f, out)
	require.NoError(t, err)
	info, err := os.Stat(filepath.Join(out, "driver.so"))
	require.NoError(t, err)
	assert.Zero(t, info.Mode().Perm()&0o111)
	assert.Zero(t, info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky))
	baseline, err := os.Create(filepath.Join(out, "mode-baseline"))
	require.NoError(t, err)
	baselineInfo, err := baseline.Stat()
	require.NoError(t, err)
	require.NoError(t, baseline.Close())
	assert.Equal(t, baselineInfo.Mode().Perm(), info.Mode().Perm())
}

func TestInflateTarballPreservesExistingFileMode(t *testing.T) {
	f := writePackageArchive(t, packageFile("MANIFEST", ""), packageFile("driver.so", "x"))
	out := t.TempDir()
	driverPath := filepath.Join(out, "driver.so")
	require.NoError(t, os.WriteFile(driverPath, []byte("old"), 0o600))
	_, err := InflateTarball(f, out)
	require.NoError(t, err)
	info, err := os.Stat(driverPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
