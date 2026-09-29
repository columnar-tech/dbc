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
	"reflect"
	"testing"
)

func TestSplitConfigListKeepsWasmLocationAsOnePath(t *testing.T) {
	for _, path := range []string{
		"C:/drivers",
		"C:/drivers:alternate",
		"//server/share/drivers",
		"drivers;alternate",
	} {
		if got := splitConfigList(path); !reflect.DeepEqual(got, []string{path}) {
			t.Errorf("splitConfigList(%q) = %#v, want one unchanged path", path, got)
		}
	}
	if got := splitConfigList(""); got != nil {
		t.Errorf("splitConfigList(empty) = %#v, want nil", got)
	}
}
