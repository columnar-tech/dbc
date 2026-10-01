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

// Package hostpath provides host-filesystem path operations for runtimes whose
// path syntax differs from the Go target's filepath syntax, such as Node on
// Windows running a js/wasm binary.
package hostpath

import "os"

func Abs(path string) (string, error)               { return abs(path) }
func IsAbs(path string) bool                        { return isAbs(path) }
func Clean(path string) string                      { return clean(path) }
func Join(elem ...string) string                    { return join(elem...) }
func Dir(path string) string                        { return dir(path) }
func Base(path string) string                       { return base(path) }
func Rel(basepath, targpath string) (string, error) { return rel(basepath, targpath) }
func EvalSymlinks(path string) (string, error)      { return evalSymlinks(path) }
func Equal(a, b string) bool                        { return equal(a, b) }
func MkdirAll(path string, perm os.FileMode) error  { return mkdirAll(path, perm) }
func ListSeparator() rune                           { return listSeparator() }
func IsWindows() bool                               { return isWindows() }
func Separator() string                             { return separator() }
