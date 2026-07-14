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

//go:build js

package config

import (
	"os"
	"path/filepath"
	"strings"
)

// loadDir lists installed drivers from a directory. Under GOOS=js, os.ReadDir
// and os.DirFS fail on Windows hosts because Go's js/wasm syscall layer rejects
// the O_DIRECTORY flag (Node.js does not expose it on Windows). We work around
// this by using os.Open (which omits O_DIRECTORY) followed by File.ReadDir.
func loadDir(dir string) (map[string]DriverInfo, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return nil, err
	}

	ret := make(map[string]DriverInfo)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		di, err := loadDriverFromManifest(filepath.Dir(p), filepath.Base(p))
		if err != nil {
			continue
		}
		di.FilePath = filepath.Dir(p)
		ret[di.ID] = di
	}
	return ret, nil
}
