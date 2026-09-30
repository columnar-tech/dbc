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

// Package wintest contains Windows-only test fixture support. The fixture
// supplies private roots to dbc's own registry and filesystem code without
// changing the registry view used by Windows system APIs.
package wintest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/columnar-tech/dbc/internal/systempath"
	"github.com/columnar-tech/dbc/internal/winregroot"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const regProcessAppKey = 0x00000001

var clearForTestsEnvironment = []string{"ADBC_DRIVER_PATH", "VIRTUAL_ENV", "CONDA_PREFIX"}

var (
	advapi32DLL        = windows.NewLazySystemDLL("advapi32.dll")
	regLoadAppKeyWProc = advapi32DLL.NewProc("RegLoadAppKeyW")
)

// Isolation supplies separate private registry handles and filesystem roots to
// dbc's own User/System registration code. It does not change Windows system
// APIs' registry view or exercise real Program Files/HKLM ACLs.
type Isolation struct {
	tempDir             string
	appData             string
	systemRoot          string
	priorAppData        string
	hadAppData          bool
	priorEnv            []environmentValue
	appHive             registry.Key
	userHive            registry.Key
	systemHive          registry.Key
	restoreRoot         func()
	restoreRegistryRoot func()
}

type environmentValue struct {
	name    string
	value   string
	present bool
}

// Setup creates the process-private Windows test fixture. Any setup failure
// rolls back all state before returning, so callers can fail closed.
func Setup() (*Isolation, error) {
	isolation := &Isolation{}
	isolation.priorAppData, isolation.hadAppData = os.LookupEnv("APPDATA")
	for _, name := range clearForTestsEnvironment {
		value, present := os.LookupEnv(name)
		isolation.priorEnv = append(isolation.priorEnv, environmentValue{name: name, value: value, present: present})
	}

	dir, err := os.MkdirTemp("", "dbc-windows-tests-")
	if err != nil {
		return nil, fmt.Errorf("create private Windows test directory: %w", err)
	}
	isolation.tempDir = dir
	if err := isolation.setup(); err != nil {
		return nil, errors.Join(err, isolation.Cleanup())
	}
	return isolation, nil
}

func (isolation *Isolation) setup() error {
	for _, name := range clearForTestsEnvironment {
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("clear %s for isolated Windows tests: %w", name, err)
		}
	}

	isolation.appData = filepath.Join(isolation.tempDir, "user")
	isolation.systemRoot = filepath.Join(isolation.tempDir, "system")
	for _, path := range []string{isolation.appData, isolation.systemRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create private Windows test root %q: %w", path, err)
		}
	}
	if err := os.Setenv("APPDATA", isolation.appData); err != nil {
		return fmt.Errorf("set private APPDATA: %w", err)
	}
	isolation.restoreRoot = systempath.SetProgramFilesRootForTests(isolation.systemRoot)

	userConfigRoot, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolve private User config root: %w", err)
	}
	wantUserRoot := filepath.Join(isolation.appData, "ADBC", "Drivers")
	wantSystemRoot := filepath.Join(isolation.systemRoot, "ADBC", "Drivers")
	if got := filepath.Join(userConfigRoot, "ADBC", "Drivers"); !samePath(got, wantUserRoot) {
		return fmt.Errorf("private User driver root = %q, want %q", got, wantUserRoot)
	}
	if got := filepath.Join(systempath.ProgramFilesRoot(), "ADBC", "Drivers"); !samePath(got, wantSystemRoot) {
		return fmt.Errorf("private System driver root = %q, want %q", got, wantSystemRoot)
	}

	hivePath := filepath.Join(isolation.tempDir, "registry.hiv")
	if _, err := os.Lstat(hivePath); err == nil || !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = errors.New("private registry hive path already exists")
		}
		return fmt.Errorf("verify private registry hive path: %w", err)
	}
	isolation.appHive, err = loadPrivateRegistryHive(hivePath)
	if err != nil {
		return err
	}
	isolation.userHive, _, err = registry.CreateKey(isolation.appHive, "User", registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("create private HKCU hive root: %w", err)
	}
	isolation.systemHive, _, err = registry.CreateKey(isolation.appHive, "System", registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("create private HKLM hive root: %w", err)
	}
	isolation.restoreRegistryRoot = winregroot.SetRootsForTests(isolation.userHive, isolation.systemHive)
	if err := verifyRegistryRoot(winregroot.UserRoot(), isolation.userHive, "user"); err != nil {
		return err
	}
	if err := verifyRegistryRoot(winregroot.SystemRoot(), isolation.systemHive, "system"); err != nil {
		return err
	}
	return nil
}

// UserRoot returns the private ConfigUser-compatible driver directory.
func (isolation *Isolation) UserRoot() string {
	return filepath.Join(isolation.appData, "ADBC", "Drivers")
}

// SystemRoot returns the private ConfigSystem-compatible driver directory.
func (isolation *Isolation) SystemRoot() string {
	return filepath.Join(isolation.systemRoot, "ADBC", "Drivers")
}

// Cleanup restores dbc's registry roots and APPDATA, closes all hive handles,
// restores the production system root, and removes the fixture.
func (isolation *Isolation) Cleanup() error {
	var cleanupErr error
	if isolation.restoreRegistryRoot != nil {
		isolation.restoreRegistryRoot()
		isolation.restoreRegistryRoot = nil
	}
	if isolation.systemHive != 0 {
		cleanupErr = errors.Join(cleanupErr, isolation.systemHive.Close())
		isolation.systemHive = 0
	}
	if isolation.userHive != 0 {
		cleanupErr = errors.Join(cleanupErr, isolation.userHive.Close())
		isolation.userHive = 0
	}
	if isolation.appHive != 0 {
		cleanupErr = errors.Join(cleanupErr, isolation.appHive.Close())
		isolation.appHive = 0
	}
	if isolation.restoreRoot != nil {
		isolation.restoreRoot()
		isolation.restoreRoot = nil
	}
	if isolation.hadAppData {
		cleanupErr = errors.Join(cleanupErr, os.Setenv("APPDATA", isolation.priorAppData))
	} else {
		cleanupErr = errors.Join(cleanupErr, os.Unsetenv("APPDATA"))
	}
	if isolation.tempDir != "" {
		cleanupErr = errors.Join(cleanupErr, os.RemoveAll(isolation.tempDir))
		isolation.tempDir = ""
	}
	for _, prior := range isolation.priorEnv {
		if prior.present {
			cleanupErr = errors.Join(cleanupErr, os.Setenv(prior.name, prior.value))
		} else {
			cleanupErr = errors.Join(cleanupErr, os.Unsetenv(prior.name))
		}
	}
	return cleanupErr
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

func verifyRegistryRoot(root, privateRoot registry.Key, name string) error {
	probePath := "Software\\dbc-test-isolation-probe"
	want := "private-" + name
	privateKey, _, err := registry.CreateKey(privateRoot, probePath, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("create private %s probe: %w", name, err)
	}
	writeErr := privateKey.SetStringValue("probe", want)
	closeErr := privateKey.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write private %s probe: %w", name, err)
	}
	viewKey, err := registry.OpenKey(root, probePath, registry.READ)
	if err != nil {
		return fmt.Errorf("read private %s probe through dbc registry root: %w", name, err)
	}
	got, _, readErr := viewKey.GetStringValue("probe")
	closeErr = viewKey.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read private %s probe through dbc registry root: %w", name, err)
	}
	if got != want {
		return fmt.Errorf("%s isolation probe = %q, want %q", name, got, want)
	}
	if err := registry.DeleteKey(privateRoot, probePath); err != nil {
		return fmt.Errorf("remove private %s probe: %w", name, err)
	}
	return nil
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
