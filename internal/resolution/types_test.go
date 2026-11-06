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

package resolution

import "testing"

func TestTargetFromPlatformTuple(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Target
	}{
		{"linux defaults to gnu", "linux_amd64", Target{OS: "linux", Arch: "amd64", LibC: "gnu"}},
		{"x86_64 alias and explicit musl", "linux_x86_64_musl_v3", Target{OS: "linux", Arch: "amd64", LibC: "musl", Variant: "v3"}},
		{"darwin and aarch64 aliases", "darwin_aarch64", Target{OS: "macos", Arch: "arm64"}},
		{"remaining components become variant", "linux_amd64_gnu_abi_v2", Target{OS: "linux", Arch: "amd64", LibC: "gnu", Variant: "abi_v2"}},
		{"unknown tokens are preserved", "plan9_riscv128", Target{OS: "plan9", Arch: "riscv128"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TargetFromPlatformTuple(tt.input)
			if err != nil {
				t.Fatalf("TargetFromPlatformTuple(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("TargetFromPlatformTuple(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestTargetFromPlatformTupleRejectsInvalidTokens(t *testing.T) {
	for _, tuple := range []string{"", "linux", "linux__amd64", "Linux_amd64", "linux_amd64_musl_gnu"} {
		t.Run(tuple, func(t *testing.T) {
			if _, err := TargetFromPlatformTuple(tuple); err == nil {
				t.Fatalf("TargetFromPlatformTuple(%q) unexpectedly succeeded", tuple)
			}
		})
	}
}

func TestCanonicalTargetAndValidateConcreteTarget(t *testing.T) {
	if got := CanonicalTarget(Target{OS: "darwin", Arch: "aarch64"}); got != (Target{OS: "macos", Arch: "arm64"}) {
		t.Fatalf("CanonicalTarget aliases = %#v", got)
	}
	if got := CanonicalTarget(Target{OS: "plan9", Arch: "riscv128", Variant: "future"}); got != (Target{OS: "plan9", Arch: "riscv128", Variant: "future"}) {
		t.Fatalf("CanonicalTarget guessed unknown tokens: %#v", got)
	}
	if err := ValidateConcreteTarget(Target{OS: "linux", Arch: "amd64", LibC: "gnu", Variant: "v3.1-x"}); err != nil {
		t.Fatalf("valid concrete target rejected: %v", err)
	}
	for _, target := range []Target{
		{OS: "linux!", Arch: "amd64"},
		{OS: "linux", Arch: ""},
		{OS: "linux", Arch: "amd64", Variant: "bad/value"},
	} {
		if err := ValidateConcreteTarget(target); err == nil {
			t.Errorf("ValidateConcreteTarget(%#v) unexpectedly succeeded", target)
		}
	}
}

func TestValidateLocalPath(t *testing.T) {
	for _, value := range []string{"archives/driver.tar.gz", "../driver.tar.gz", "/opt/driver.tar.gz", `C:\drivers\driver.tar.gz`} {
		if err := ValidateLocalPath(value); err != nil {
			t.Errorf("ValidateLocalPath(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", "driver\x00.tar.gz", "https://example.com/driver.tar.gz", "file:///driver.tar.gz"} {
		if err := ValidateLocalPath(value); err == nil {
			t.Errorf("ValidateLocalPath(%q) unexpectedly succeeded", value)
		}
	}
}
