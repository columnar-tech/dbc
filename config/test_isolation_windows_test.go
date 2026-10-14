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

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc/internal/wintest"
)

func TestMain(m *testing.M) {
	isolation, err := wintest.Setup()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "set up isolated Windows config tests: %v\n", err)
		os.Exit(1)
	}
	if err := verifyWindowsConfigRoots(isolation); err != nil {
		cleanupErr := isolation.Cleanup()
		_, _ = fmt.Fprintf(os.Stderr, "verify isolated Windows config roots: %v\n", errors.Join(err, cleanupErr))
		os.Exit(1)
	}

	code := m.Run()
	if err := isolation.Cleanup(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "clean up isolated Windows config tests: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func verifyWindowsConfigRoots(isolation *wintest.Isolation) error {
	if got, want := filepath.Clean(ConfigUser.ConfigLocation()), filepath.Clean(isolation.UserRoot()); !strings.EqualFold(got, want) {
		return fmt.Errorf("ConfigUser root = %q, want private root %q", got, want)
	}
	if got, want := filepath.Clean(ConfigSystem.ConfigLocation()), filepath.Clean(isolation.SystemRoot()); !strings.EqualFold(got, want) {
		return fmt.Errorf("ConfigSystem root = %q, want private root %q", got, want)
	}
	return nil
}
