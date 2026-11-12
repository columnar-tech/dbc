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

package internal

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGetUserCachePath(t *testing.T) {
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache directory: %v", err)
	}
	got, err := GetUserCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, userDirName(), "dbc"); got != want {
		t.Errorf("GetUserCachePath() = %q, want %q", got, want)
	}
}

func TestGetUserCachePathXDG(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_CACHE_HOME applies on Linux")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	got, err := GetUserCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cache, "columnar", "dbc"); got != want {
		t.Errorf("GetUserCachePath() = %q, want %q", got, want)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Errorf("GetUserCachePath created %q: %v", got, err)
	}

	t.Setenv("XDG_CACHE_HOME", "relative/cache")
	if got, err := GetUserCachePath(); err == nil {
		t.Errorf("relative XDG_CACHE_HOME accepted: %q", got)
	}
}
