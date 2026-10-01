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

package hostpath

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall/js"
)

func adapter() js.Value {
	v := js.Global().Get("dbcHostPath")
	if v.Type() != js.TypeObject {
		return js.Undefined()
	}
	return v
}

func call(name string, args ...any) (js.Value, bool) {
	a := adapter()
	if a.Type() != js.TypeObject {
		return js.Undefined(), false
	}
	f := a.Get(name)
	if f.Type() != js.TypeFunction {
		return js.Undefined(), false
	}
	values := make([]any, len(args))
	copy(values, args)
	result := f.Invoke(values...)
	if result.Type() != js.TypeObject {
		return js.Undefined(), false
	}
	if message := result.Get("error"); message.Type() == js.TypeString && message.String() != "" {
		return result, true
	}
	return result, true
}

func stringCall(name string, args ...any) (string, bool, error) {
	result, ok := call(name, args...)
	if !ok {
		return "", false, nil
	}
	if message := result.Get("error"); message.Type() == js.TypeString && message.String() != "" {
		return "", true, fmt.Errorf("%s", message.String())
	}
	value := result.Get("value")
	if value.Type() != js.TypeString {
		return "", true, fmt.Errorf("host path adapter %s returned a non-string value", name)
	}
	return value.String(), true, nil
}

func boolCall(name string, args ...any) (bool, bool) {
	result, ok := call(name, args...)
	if !ok || result.Get("error").Type() == js.TypeString && result.Get("error").String() != "" {
		return false, false
	}
	value := result.Get("value")
	if value.Type() != js.TypeBoolean {
		return false, false
	}
	return value.Bool(), true
}

func abs(path string) (string, error) {
	if value, ok, err := stringCall("abs", path); ok {
		return value, err
	}
	return filepath.Abs(path)
}

func isAbs(path string) bool {
	if value, ok := boolCall("isAbs", path); ok {
		return value
	}
	return filepath.IsAbs(path)
}

func clean(path string) string {
	if value, ok, err := stringCall("clean", path); ok && err == nil {
		return value
	}
	return filepath.Clean(path)
}

func join(elem ...string) string {
	args := make([]any, len(elem))
	for i := range elem {
		args[i] = elem[i]
	}
	if value, ok, err := stringCall("join", args...); ok && err == nil {
		return value
	}
	return filepath.Join(elem...)
}

func dir(path string) string {
	if value, ok, err := stringCall("dir", path); ok && err == nil {
		return value
	}
	return filepath.Dir(path)
}

func base(path string) string {
	if value, ok, err := stringCall("base", path); ok && err == nil {
		return value
	}
	return filepath.Base(path)
}

func rel(basepath, targpath string) (string, error) {
	if value, ok, err := stringCall("rel", basepath, targpath); ok {
		return value, err
	}
	return filepath.Rel(basepath, targpath)
}

func evalSymlinks(path string) (string, error) {
	if value, ok, err := stringCall("evalSymlinks", path); ok {
		return value, err
	}
	return filepath.EvalSymlinks(path)
}

func equal(a, b string) bool {
	if value, ok := boolCall("equal", a, b); ok {
		return value
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func mkdirAll(path string, perm os.FileMode) error {
	if result, ok := call("mkdirAll", path, int(perm)); ok {
		if message := result.Get("error"); message.Type() == js.TypeString && message.String() != "" {
			return fmt.Errorf("%s", message.String())
		}
		return nil
	}
	return os.MkdirAll(path, perm)
}

func listSeparator() rune {
	if value, ok := boolCall("isWindows"); ok && value {
		return ';'
	}
	return filepath.ListSeparator
}

func isWindows() bool {
	value, ok := boolCall("isWindows")
	return ok && value
}

func separator() string {
	if isWindows() {
		return "/"
	}
	return string(filepath.Separator)
}
