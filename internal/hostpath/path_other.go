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

package hostpath

import (
	"os"
	"path/filepath"
	"strings"
)

func abs(path string) (string, error)               { return filepath.Abs(path) }
func isAbs(path string) bool                        { return filepath.IsAbs(path) }
func clean(path string) string                      { return filepath.Clean(path) }
func join(elem ...string) string                    { return filepath.Join(elem...) }
func dir(path string) string                        { return filepath.Dir(path) }
func base(path string) string                       { return filepath.Base(path) }
func rel(basepath, targpath string) (string, error) { return filepath.Rel(basepath, targpath) }
func evalSymlinks(path string) (string, error)      { return filepath.EvalSymlinks(path) }
func equal(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}
func mkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func listSeparator() rune                          { return filepath.ListSeparator }
func isWindows() bool                              { return filepath.Separator == '\\' }
func separator() string                            { return string(filepath.Separator) }
