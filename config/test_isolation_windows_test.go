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
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	regProcessAppKey              = 0x00000001
	windowsIsolationChildEnv      = "DBC_CONFIG_ISOLATION_CHILD"
	windowsIsolationParentRootEnv = "DBC_CONFIG_ISOLATION_PARENT_ROOT"
	windowsIsolationDriverID      = "dbc-process-isolation-sentinel"
	windowsIsolationRelativePath  = ".dbc-package-g-process-isolation/driver.dll"
)

var (
	advapi32DLL              = windows.NewLazySystemDLL("advapi32.dll")
	regLoadAppKeyWProc       = advapi32DLL.NewProc("RegLoadAppKeyW")
	regOverridePredefKeyProc = advapi32DLL.NewProc("RegOverridePredefKey")
)

type windowsTestIsolation struct {
	tempDir      string
	appData      string
	priorAppData string
	hadAppData   bool
	appHive      registry.Key
	userHive     registry.Key
	overridden   bool
}

func TestMain(m *testing.M) {
	isolation, err := setupWindowsTestIsolation()
	if err != nil {
		if isolation != nil {
			err = errors.Join(err, isolation.cleanup())
		}
		_, _ = fmt.Fprintf(os.Stderr, "set up isolated Windows config tests: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	if err := isolation.cleanup(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "clean up isolated Windows config tests: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func setupWindowsTestIsolation() (*windowsTestIsolation, error) {
	isolation := &windowsTestIsolation{}
	isolation.priorAppData, isolation.hadAppData = os.LookupEnv("APPDATA")

	dir, err := os.MkdirTemp("", "dbc-config-windows-test-")
	if err != nil {
		return isolation, fmt.Errorf("create private test directory: %w", err)
	}
	isolation.tempDir = dir
	isolation.appData = filepath.Join(dir, "appdata")
	if err := os.Mkdir(isolation.appData, 0o700); err != nil {
		return isolation, fmt.Errorf("create private APPDATA directory: %w", err)
	}
	if err := os.Setenv("APPDATA", isolation.appData); err != nil {
		return isolation, fmt.Errorf("set private APPDATA: %w", err)
	}
	wantConfigLocation := filepath.Join(isolation.appData, "ADBC", "Drivers")
	if got := ConfigUser.ConfigLocation(); !strings.EqualFold(filepath.Clean(got), filepath.Clean(wantConfigLocation)) {
		return isolation, fmt.Errorf("ConfigUser location = %q, want private APPDATA location %q", got, wantConfigLocation)
	}

	hivePath := filepath.Join(dir, "hkcu.hiv")
	if _, err := os.Lstat(hivePath); err == nil || !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = errors.New("private registry hive path already exists")
		}
		return isolation, fmt.Errorf("verify private registry hive path: %w", err)
	}
	isolation.appHive, err = loadPrivateRegistryHive(hivePath)
	if err != nil {
		return isolation, err
	}
	isolation.userHive, _, err = registry.CreateKey(isolation.appHive, "User", registry.ALL_ACCESS)
	if err != nil {
		return isolation, fmt.Errorf("create private HKCU hive root: %w", err)
	}
	if err := overrideCurrentUserRegistry(isolation.userHive); err != nil {
		return isolation, err
	}
	isolation.overridden = true
	if err := verifyCurrentUserRegistryOverride(isolation); err != nil {
		return isolation, err
	}
	return isolation, nil
}

func loadPrivateRegistryHive(path string) (registry.Key, error) {
	filename, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("encode private registry hive path: %w", err)
	}
	var hive uintptr
	status, _, _ := regLoadAppKeyWProc.Call(
		uintptr(unsafe.Pointer(filename)),
		uintptr(unsafe.Pointer(&hive)),
		uintptr(registry.ALL_ACCESS),
		regProcessAppKey,
		0,
	)
	if status != 0 {
		return 0, fmt.Errorf("RegLoadAppKeyW: %w", windows.Errno(status))
	}
	return registry.Key(hive), nil
}

func overrideCurrentUserRegistry(userHive registry.Key) error {
	status, _, _ := regOverridePredefKeyProc.Call(uintptr(registry.CURRENT_USER), uintptr(userHive))
	if status != 0 {
		return fmt.Errorf("RegOverridePredefKey(HKCU): %w", windows.Errno(status))
	}
	return nil
}

func resetCurrentUserRegistryOverride() error {
	status, _, _ := regOverridePredefKeyProc.Call(uintptr(registry.CURRENT_USER), 0)
	if status != 0 {
		return fmt.Errorf("RegOverridePredefKey(HKCU, nil): %w", windows.Errno(status))
	}
	return nil
}

func verifyCurrentUserRegistryOverride(isolation *windowsTestIsolation) error {
	probeName := "Software\\dbc-isolation-probe-" + filepath.Base(isolation.tempDir)
	want := "private-hive"
	privateKey, _, err := registry.CreateKey(isolation.userHive, probeName, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("create registry isolation probe: %w", err)
	}
	if err := privateKey.SetStringValue("probe", want); err != nil {
		_ = privateKey.Close()
		return fmt.Errorf("write registry isolation probe: %w", err)
	}
	if err := privateKey.Close(); err != nil {
		return fmt.Errorf("close registry isolation probe: %w", err)
	}
	viewKey, err := registry.OpenKey(registry.CURRENT_USER, probeName, registry.READ)
	if err != nil {
		return fmt.Errorf("read registry isolation probe through HKCU: %w", err)
	}
	got, _, readErr := viewKey.GetStringValue("probe")
	closeErr := viewKey.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("read registry isolation probe through HKCU: %w", errors.Join(readErr, closeErr))
	}
	if got != want {
		return fmt.Errorf("registry isolation probe = %q, want %q", got, want)
	}
	if err := registry.DeleteKey(isolation.userHive, probeName); err != nil {
		return fmt.Errorf("remove registry isolation probe: %w", err)
	}
	return nil
}

func (isolation *windowsTestIsolation) cleanup() error {
	var cleanupErr error
	if isolation.overridden {
		cleanupErr = errors.Join(cleanupErr, resetCurrentUserRegistryOverride())
		isolation.overridden = false
	}
	if isolation.userHive != 0 {
		cleanupErr = errors.Join(cleanupErr, isolation.userHive.Close())
		isolation.userHive = 0
	}
	if isolation.appHive != 0 {
		cleanupErr = errors.Join(cleanupErr, isolation.appHive.Close())
		isolation.appHive = 0
	}
	if isolation.hadAppData {
		cleanupErr = errors.Join(cleanupErr, os.Setenv("APPDATA", isolation.priorAppData))
	} else {
		cleanupErr = errors.Join(cleanupErr, os.Unsetenv("APPDATA"))
	}
	if isolation.tempDir != "" {
		cleanupErr = errors.Join(cleanupErr, os.RemoveAll(isolation.tempDir))
	}
	return cleanupErr
}

func TestWindowsConfigRegistryIsolationAcrossProcesses(t *testing.T) {
	parentRoot := ConfigUser.ConfigLocation()
	parentShared := filepath.Join(parentRoot, filepath.FromSlash(windowsIsolationRelativePath))
	t.Cleanup(func() {
		cleanupErr := clearWindowsIsolationDriver(windowsIsolationDriverID)
		cleanupErr = errors.Join(cleanupErr, os.RemoveAll(filepath.Dir(parentShared)))
		if cleanupErr != nil {
			t.Errorf("remove parent process-isolation fixture: %v", cleanupErr)
		}
	})
	parentBytes := []byte("parent registry and payload sentinel")
	if err := os.MkdirAll(filepath.Dir(parentShared), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parentShared, parentBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	writeWindowsIsolationDriver(t, windowsIsolationDriverID, parentShared)

	child := exec.Command(os.Args[0], "-test.run=^TestWindowsConfigRegistryIsolationChild$")
	child.Env = append(os.Environ(), windowsIsolationChildEnv+"=1", windowsIsolationParentRootEnv+"="+parentRoot)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated child process failed: %v\n%s", err, output)
	}

	if got, err := os.ReadFile(parentShared); err != nil || !bytes.Equal(got, parentBytes) {
		t.Fatalf("child cleanup changed parent APPDATA sentinel: %q, %v", got, err)
	}
	if got := readWindowsIsolationDriver(t, windowsIsolationDriverID); got != parentShared {
		t.Fatalf("child cleanup changed parent HKCU registration path: %q, want %q", got, parentShared)
	}
}

func TestWindowsConfigRegistryIsolationChild(t *testing.T) {
	if os.Getenv(windowsIsolationChildEnv) != "1" {
		t.Skip("child-process isolation fixture")
	}

	parentRoot := os.Getenv(windowsIsolationParentRootEnv)
	childRoot := ConfigUser.ConfigLocation()
	if filepath.Clean(childRoot) == filepath.Clean(parentRoot) {
		t.Fatalf("child APPDATA root %q is not private from parent root %q", childRoot, parentRoot)
	}
	childShared := filepath.Join(childRoot, filepath.FromSlash(windowsIsolationRelativePath))
	if err := os.MkdirAll(filepath.Dir(childShared), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childShared, []byte("child registry and payload sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWindowsIsolationDriver(t, windowsIsolationDriverID, childShared)
	// Exercise the broad cleanup this private process boundary is designed to
	// contain. The process has its own APPDATA tree and private HKCU hive.
	if err := clearWindowsIsolationRegistry(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(childRoot); err != nil {
		t.Fatalf("remove child APPDATA driver root: %v", err)
	}
}

func writeWindowsIsolationDriver(t *testing.T, id, shared string) {
	t.Helper()
	root, _, err := registry.CreateKey(registry.CURRENT_USER, regKeyADBC, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	driver, _, err := registry.CreateKey(root, id, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = driver.Close() }()
	if err := driver.SetStringValue("driver", shared); err != nil {
		t.Fatal(err)
	}
	if err := driver.SetStringValue("version", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := driver.SetStringValue("name", "Process Isolation Sentinel"); err != nil {
		t.Fatal(err)
	}
}

func readWindowsIsolationDriver(t *testing.T, id string) string {
	t.Helper()
	driver, err := registry.OpenKey(registry.CURRENT_USER, regKeyADBC+"\\"+id, registry.READ)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = driver.Close() }()
	shared, _, err := driver.GetStringValue("driver")
	if err != nil {
		t.Fatal(err)
	}
	return shared
}

func clearWindowsIsolationRegistry() error {
	root, err := registry.OpenKey(registry.CURRENT_USER, regKeyADBC, registry.ALL_ACCESS)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	names, readErr := root.ReadSubKeyNames(-1)
	for _, name := range names {
		readErr = errors.Join(readErr, registry.DeleteKey(root, name))
	}
	closeErr := root.Close()
	return errors.Join(readErr, closeErr)
}

func clearWindowsIsolationDriver(id string) error {
	root, err := registry.OpenKey(registry.CURRENT_USER, regKeyADBC, registry.ALL_ACCESS)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	deleteErr := registry.DeleteKey(root, id)
	closeErr := root.Close()
	if errors.Is(deleteErr, registry.ErrNotExist) {
		deleteErr = nil
	}
	return errors.Join(deleteErr, closeErr)
}
