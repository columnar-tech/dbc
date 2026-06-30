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

package config

import (
	"io/fs"
	"os"
	"path/filepath"
)

func loadDir(dir string) (map[string]DriverInfo, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}

	ret := make(map[string]DriverInfo)

	fsys := os.DirFS(dir)
	matches, _ := fs.Glob(fsys, "*.toml")
	for _, m := range matches {
		p := filepath.Join(dir, m)
		di, err := loadDriverFromManifest(filepath.Dir(p), filepath.Base(p))
		if err != nil {
			continue
		}

		di.FilePath = filepath.Dir(p)
		ret[di.ID] = di
	}
	return ret, nil
}
