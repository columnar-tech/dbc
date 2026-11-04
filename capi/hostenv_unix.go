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

//go:build unix

package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"unsafe"

	"github.com/columnar-tech/dbc/internal/hostenv"
)

// The host (C, Rust, Python, ...) changes its environment through libc
// (setenv, putenv), which Go's startup copy does not see; read libc's environ
// instead. Windows needs no override: Go reads the live process environment
// there, which the CRT's _putenv and SetEnvironmentVariable both update.
func init() {
	hostenv.SetLookup(func(key string) (string, bool) {
		k := C.CString(key)
		defer C.free(unsafe.Pointer(k))
		v := C.getenv(k)
		if v == nil {
			return "", false
		}
		return C.GoString(v), true
	})
}
