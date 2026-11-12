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

//go:build !js

package internal

import (
	"fmt"
	"os"
	"path/filepath"
)

// GetUserCachePath returns dbc's directory for regenerable cache data:
// <os.UserCacheDir()>/<columnar|Columnar>/dbc, named like GetUserConfigPath.
// It only resolves the path and does not create it.
//
// It is native only: under GOOS=js, os.UserCacheDir does not follow the Node
// host's conventions the way UserConfigDir does. Data that cannot be
// regenerated, such as remembered trust state, does not belong here.
func GetUserCachePath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user cache directory: %w", err)
	}
	return filepath.Join(base, userDirName(), "dbc"), nil
}
