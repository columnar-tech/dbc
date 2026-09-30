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

//go:build windows

package wintest

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

func TestPredefinedRegistryKeyArgumentUsesWindowsABIValue(t *testing.T) {
	wantUser := uint64(0x80000001)
	wantMachine := uint64(0x80000002)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wantUser = 0xffffffff80000001
		wantMachine = 0xffffffff80000002
	}

	if got := uint64(predefinedRegistryKeyArgument(registry.CURRENT_USER)); got != wantUser {
		t.Fatalf("HKCU ABI argument = %#x, want %#x", got, wantUser)
	}
	if got := uint64(predefinedRegistryKeyArgument(registry.LOCAL_MACHINE)); got != wantMachine {
		t.Fatalf("HKLM ABI argument = %#x, want %#x", got, wantMachine)
	}
}
