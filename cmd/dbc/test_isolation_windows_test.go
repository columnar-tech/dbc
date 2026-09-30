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

package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/winregroot"
	"github.com/columnar-tech/dbc/internal/wintest"
	"golang.org/x/sys/windows/registry"
)

const (
	windowsTestIsolationChildEnv = "DBC_WINDOWS_TEST_ISOLATION_CHILD"
	windowsTestIsolationUserEnv  = "DBC_WINDOWS_TEST_ISOLATION_PARENT_USER"
	windowsTestIsolationSysEnv   = "DBC_WINDOWS_TEST_ISOLATION_PARENT_SYSTEM"
	windowsTestIsolationDriverID = "dbc-process-isolation-sentinel"
	windowsTestIsolationRelative = ".dbc-package-g-process-isolation/driver.dll"
)

func TestMain(m *testing.M) {
	isolation, err := wintest.Setup()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "set up isolated Windows CLI tests: %v\n", err)
		os.Exit(1)
	}
	if err := verifyWindowsCLIConfigRoots(isolation); err != nil {
		cleanupErr := isolation.Cleanup()
		_, _ = fmt.Fprintf(os.Stderr, "verify isolated Windows CLI roots: %v\n", errors.Join(err, cleanupErr))
		os.Exit(1)
	}

	code := m.Run()
	if err := isolation.Cleanup(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "clean up isolated Windows CLI tests: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func verifyWindowsCLIConfigRoots(isolation *wintest.Isolation) error {
	if got, want := filepath.Clean(config.ConfigUser.ConfigLocation()), filepath.Clean(isolation.UserRoot()); !strings.EqualFold(got, want) {
		return fmt.Errorf("ConfigUser root = %q, want private root %q", got, want)
	}
	if got, want := filepath.Clean(config.ConfigSystem.ConfigLocation()), filepath.Clean(isolation.SystemRoot()); !strings.EqualFold(got, want) {
		return fmt.Errorf("ConfigSystem root = %q, want private root %q", got, want)
	}
	return nil
}

func TestWindowsTestIsolationAcrossProcesses(t *testing.T) {
	if os.Getenv(windowsTestIsolationChildEnv) == "1" {
		runWindowsTestIsolationChild(t)
		return
	}

	parentUserRoot := config.ConfigUser.ConfigLocation()
	parentSystemRoot := config.ConfigSystem.ConfigLocation()
	parentUserPath := windowsIsolationPayload(parentUserRoot)
	parentSystemPath := windowsIsolationPayload(parentSystemRoot)
	parentUserBytes := []byte("parent user registry and payload sentinel")
	parentSystemBytes := []byte("parent system registry and payload sentinel")
	writeIsolationPayload(t, parentUserPath, parentUserBytes)
	writeIsolationPayload(t, parentSystemPath, parentSystemBytes)
	writeIsolationRegistration(t, winregroot.UserRoot(), parentUserPath)
	writeIsolationRegistration(t, winregroot.SystemRoot(), parentSystemPath)
	t.Setenv("ADBC_DRIVER_PATH", parentUserRoot)
	t.Setenv("VIRTUAL_ENV", parentSystemRoot)
	t.Setenv("CONDA_PREFIX", parentSystemRoot)
	t.Cleanup(func() {
		var cleanupErr error
		cleanupErr = errors.Join(cleanupErr, deleteRegistryKeyRecursive(winregroot.UserRoot(), "SOFTWARE\\ADBC\\Drivers\\"+windowsTestIsolationDriverID))
		cleanupErr = errors.Join(cleanupErr, deleteRegistryKeyRecursive(winregroot.SystemRoot(), "SOFTWARE\\ADBC\\Drivers\\"+windowsTestIsolationDriverID))
		cleanupErr = errors.Join(cleanupErr, os.RemoveAll(filepath.Dir(parentUserPath)))
		cleanupErr = errors.Join(cleanupErr, os.RemoveAll(filepath.Dir(parentSystemPath)))
		if cleanupErr != nil {
			t.Errorf("remove parent isolation sentinels: %v", cleanupErr)
		}
	})

	child := exec.Command(os.Args[0], "-test.run=^TestWindowsTestIsolationAcrossProcesses$")
	child.Env = append(os.Environ(),
		windowsTestIsolationChildEnv+"=1",
		windowsTestIsolationUserEnv+"="+parentUserRoot,
		windowsTestIsolationSysEnv+"="+parentSystemRoot,
	)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated child process failed: %v\n%s", err, output)
	}

	assertIsolationPayload(t, parentUserPath, parentUserBytes)
	assertIsolationPayload(t, parentSystemPath, parentSystemBytes)
	assertIsolationRegistration(t, winregroot.UserRoot(), parentUserPath)
	assertIsolationRegistration(t, winregroot.SystemRoot(), parentSystemPath)
}

func runWindowsTestIsolationChild(t *testing.T) {
	for _, name := range []string{"ADBC_DRIVER_PATH", "VIRTUAL_ENV", "CONDA_PREFIX"} {
		if value, present := os.LookupEnv(name); present {
			t.Fatalf("child inherited unsafe %s value %q", name, value)
		}
	}
	assertLocalHTTPRoundTrip(t)

	parentUserRoot := os.Getenv(windowsTestIsolationUserEnv)
	parentSystemRoot := os.Getenv(windowsTestIsolationSysEnv)
	childUserRoot := config.ConfigUser.ConfigLocation()
	childSystemRoot := config.ConfigSystem.ConfigLocation()
	if sameWindowsPath(childUserRoot, parentUserRoot) || sameWindowsPath(childSystemRoot, parentSystemRoot) {
		t.Fatalf("child fixture roots are not private: User=%q parent=%q; System=%q parent=%q", childUserRoot, parentUserRoot, childSystemRoot, parentSystemRoot)
	}

	childUserPath := windowsIsolationPayload(childUserRoot)
	childSystemPath := windowsIsolationPayload(childSystemRoot)
	writeIsolationPayload(t, childUserPath, []byte("child user payload"))
	writeIsolationPayload(t, childSystemPath, []byte("child system payload"))
	writeIsolationRegistration(t, winregroot.UserRoot(), childUserPath)
	writeIsolationRegistration(t, winregroot.SystemRoot(), childSystemPath)

	if err := deleteRegistryKeyRecursive(winregroot.UserRoot(), "SOFTWARE\\ADBC\\Drivers"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(childUserRoot); err != nil {
		t.Fatalf("remove child User driver root: %v", err)
	}
	assertIsolationRegistrationRemoved(t, winregroot.UserRoot())
	assertIsolationPathRemoved(t, childUserRoot)
	assertIsolationPayload(t, childSystemPath, []byte("child system payload"))
	assertIsolationRegistration(t, winregroot.SystemRoot(), childSystemPath)

	if err := deleteRegistryKeyRecursive(winregroot.SystemRoot(), "SOFTWARE\\ADBC\\Drivers"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(childSystemRoot); err != nil {
		t.Fatalf("remove child System driver root: %v", err)
	}
	assertIsolationRegistrationRemoved(t, winregroot.SystemRoot())
	assertIsolationPathRemoved(t, childSystemRoot)
}

func assertLocalHTTPRoundTrip(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "local loopback works")
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("GET local httptest server: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatalf("read local httptest response: %v", err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "local loopback works" {
		t.Fatalf("local HTTP response = %d %q", response.StatusCode, body)
	}
}

func windowsIsolationPayload(root string) string {
	return filepath.Join(root, filepath.FromSlash(windowsTestIsolationRelative))
}

func writeIsolationPayload(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeIsolationRegistration(t *testing.T, root registry.Key, driver string) {
	t.Helper()
	key, _, err := registry.CreateKey(root, "SOFTWARE\\ADBC\\Drivers\\"+windowsTestIsolationDriverID, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := key.Close(); err != nil {
			t.Errorf("close isolation registration: %v", err)
		}
	}()
	for name, value := range map[string]string{
		"driver":  driver,
		"name":    "Process Isolation Sentinel",
		"version": "1.0.0",
	} {
		if err := key.SetStringValue(name, value); err != nil {
			t.Fatal(err)
		}
	}
}

func assertIsolationRegistration(t *testing.T, root registry.Key, want string) {
	t.Helper()
	key, err := registry.OpenKey(root, "SOFTWARE\\ADBC\\Drivers\\"+windowsTestIsolationDriverID, registry.READ)
	if err != nil {
		t.Fatal(err)
	}
	got, _, readErr := key.GetStringValue("driver")
	closeErr := key.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("isolation registration driver = %q, want %q", got, want)
	}
}

func assertIsolationRegistrationRemoved(t *testing.T, root registry.Key) {
	t.Helper()
	key, err := registry.OpenKey(root, "SOFTWARE\\ADBC\\Drivers\\"+windowsTestIsolationDriverID, registry.READ)
	if errors.Is(err, registry.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("check removed isolation registration: %v", err)
	}
	closeErr := key.Close()
	t.Fatalf("isolation registration remains after cleanup (close error: %v)", closeErr)
}

func assertIsolationPayload(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read isolation payload %q: %v", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("isolation payload %q = %q, want %q", path, got, want)
	}
}

func assertIsolationPathRemoved(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolation path %q remains after cleanup or could not be checked: %v", path, err)
	}
}

func sameWindowsPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
