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

//go:build js && dbcnode

package internal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall/js"
)

func storageHostOS() string {
	// Storage follows Node's host, never the driver installation target override.
	platform := js.Global().Get("process").Get("platform").String()
	if platform == "win32" {
		return "windows"
	}
	return platform
}

// UserConfigDir returns the native host's configuration base directory using
// the bounded, Windows-path-normalized environment supplied by boot.cjs.
func UserConfigDir() (string, error) {
	switch storageHostOS() {
	case "windows":
		dir := os.Getenv("AppData")
		if dir == "" {
			return "", errors.New("%AppData% is not defined")
		}
		return dir, nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home + "/Library/Application Support", nil
	default:
		return os.UserConfigDir()
	}
}

func isAbsPath(path string) bool {
	if storageHostOS() != "windows" {
		return filepath.IsAbs(path)
	}
	// boot.cjs normalizes drive paths to forward slashes and preserves UNC roots
	// as two backslashes so Go's Unix filepath.Join/Dir cannot collapse them.
	return strings.HasPrefix(path, `\\`) || len(path) >= 3 && path[1] == ':' && path[2] == '/'
}
