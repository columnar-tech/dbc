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

package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteFileReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
	require.NoError(t, WriteFile(path, []byte("new"), 0o600))

	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new", string(actual))
}

func TestWriteFileLeavesOldFileWhenReplacementFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
	errInjected := errors.New("injected replace failure")
	err := writeFile(path, []byte("new"), 0o600, func(string, string) error { return errInjected })
	require.ErrorIs(t, err, errInjected)

	actual, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "old", string(actual))
}
