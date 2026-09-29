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

package config

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"
	"unsafe"

	"github.com/Masterminds/semver/v3"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var userConfigDir string

func init() {
	userConfigDir, _ = os.UserConfigDir()
	if userConfigDir != "" {
		userConfigDir = filepath.Join(userConfigDir, "ADBC", "Drivers")
	}
}

func (c ConfigLevel) key() registry.Key {
	switch c {
	case ConfigSystem:
		return registry.LOCAL_MACHINE
	case ConfigUser:
		return registry.CURRENT_USER
	default:
		return 0
	}
}

func (c ConfigLevel) rootKeyString() string {
	switch c {
	case ConfigUser:
		return "HKCU"
	case ConfigSystem:
		return "HKLM"
	default:
		return "UNKN"
	}
}

func (c ConfigLevel) ConfigLocation() string {
	var prefix string
	switch c {
	case ConfigSystem:
		prefix = "C:\\Program Files"
	case ConfigUser:
		prefix, _ = os.UserConfigDir()
	case ConfigEnv:
		return getEnvConfigDir()
	default:
		panic("unknown config level")
	}

	return filepath.Join(prefix, "ADBC", "Drivers")
}

const (
	regKeyADBC = "SOFTWARE\\ADBC\\Drivers"
)

func keyMust(k registry.Key, name string) string {
	val, _, err := k.GetStringValue(name)
	if err != nil {
		panic(err)
	}
	return val
}

func keyIntOptional(k registry.Key, name string) uint32 {
	val, _, err := k.GetIntegerValue(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return 0
		}
		panic(err)
	}
	return uint32(val)
}

func keyOptional(k registry.Key, name string) string {
	val, _, err := k.GetStringValue(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return ""
		}
		panic(err)
	}
	return val
}

func driverInfoFromKey(k registry.Key, driverName string, lvl ConfigLevel) (di DriverInfo, err error) {
	dkey, err := registry.OpenKey(k, driverName, registry.READ)
	if err != nil {
		return di, err
	}
	defer dkey.Close()

	defer func() {
		if r := recover(); r != nil {
			switch r := r.(type) {
			case string:
				err = errors.New(r)
			case error:
				err = r
			default:
				err = fmt.Errorf("unknown error type: %v", r)
			}
		}
	}()

	ver := keyIntOptional(dkey, "manifest_version")
	if ver > currentManifestVersion {
		return DriverInfo{}, fmt.Errorf("manifest version %d is unsupported, only %d and lower are supported by this version of dbc", ver, currentManifestVersion)
	}

	di.ID = driverName
	di.Name = keyMust(dkey, "name")
	di.Publisher = keyOptional(dkey, "publisher")
	di.License = keyOptional(dkey, "license")
	di.Version = semver.MustParse(keyMust(dkey, "version"))
	di.Source = keyOptional(dkey, "source")
	di.Driver.Shared.defaultPath = keyMust(dkey, "driver")
	di.Driver.Entrypoint = keyOptional(dkey, "entrypoint")

	// For drivers in the registry, set FilePath to the registry key instead
	// of the filesystem path since that's technically where the driver exists.
	di.FilePath = fmt.Sprintf("%s\\%s", lvl.rootKeyString(), regKeyADBC)

	return
}

func loadRegistryConfig(lvl ConfigLevel) Config {
	ret := Config{Level: lvl, Location: lvl.ConfigLocation()}
	k, err := registry.OpenKey(lvl.key(), regKeyADBC, registry.READ)
	if err != nil {
		return ret
	}
	defer k.Close()

	info, err := k.Stat()
	if err != nil {
		return ret
	}

	ret.Exists, ret.Drivers = true, make(map[string]DriverInfo)
	if info.SubKeyCount == 0 {
		return ret
	}

	drivers, err := k.ReadSubKeyNames(int(info.SubKeyCount))
	if err != nil {
		log.Println(err)
		return ret
	}

	for _, driver := range drivers {
		di, err := driverInfoFromKey(k, driver, lvl)
		if err != nil {
			log.Println(err)
			continue
		}
		ret.Drivers[driver] = di
	}

	return ret
}

func Get() map[ConfigLevel]Config {
	cfgUser, cfgSys, cfgEnv := loadConfig(ConfigUser), loadConfig(ConfigSystem), loadConfig(ConfigEnv)

	regUser := loadRegistryConfig(ConfigUser)
	if regUser.Exists {
		if cfgUser.Drivers == nil {
			cfgUser.Drivers = regUser.Drivers
		} else {
			maps.Copy(cfgUser.Drivers, regUser.Drivers)
		}
	} else {
		cfgUser.Exists = false
	}

	regSys := loadRegistryConfig(ConfigSystem)
	if regSys.Exists {
		if cfgSys.Drivers == nil {
			cfgSys.Drivers = regSys.Drivers
		} else {
			maps.Copy(cfgSys.Drivers, regSys.Drivers)
		}
	} else {
		cfgSys.Exists = false
	}

	return map[ConfigLevel]Config{
		ConfigUser:   cfgUser,
		ConfigSystem: cfgSys,
		ConfigEnv:    cfgEnv,
	}
}

func FindDriverConfigs(lvl ConfigLevel) []DriverInfo {
	return slices.Collect(maps.Values(loadConfig(lvl).Drivers))
}

func GetDriver(cfg Config, driverName string) (DriverInfo, error) {
	if cfg.Level == ConfigEnv {
		for _, prefix := range filepath.SplitList(cfg.Location) {
			if di, err := loadDriverFromManifest(prefix, driverName); err == nil {
				return di, nil
			}
		}

		return DriverInfo{}, fmt.Errorf("driver `%s` not found in env config paths", driverName)
	}

	k, err := registry.OpenKey(cfg.Level.key(), regKeyADBC, registry.READ)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			switch cfg.Level {
			case ConfigUser:
				return loadDriverFromManifest(cfg.Location, driverName)
			}
		}
		return DriverInfo{}, err
	}
	defer k.Close()

	return driverInfoFromKey(k, driverName, cfg.Level)
}

func CreateManifest(cfg Config, driver DriverInfo) (err error) {
	location, err := registrationLockLocation(cfg)
	if err != nil {
		return err
	}
	if err := prepareRegistrationLockLocation(cfg, location); err != nil {
		return fmt.Errorf("prepare driver registration lock location: %w", err)
	}
	lock, err := acquireDriverInstallLockWith(context.Background(), location, driver.ID, 10*time.Second)
	if err != nil {
		return fmt.Errorf("acquire driver registration lock: %w", err)
	}
	defer func() { err = errors.Join(err, lock.release()) }()

	if cfg.Level == ConfigEnv {
		return createDriverManifestUnlocked(location, driver)
	}
	return createRegistryManifestUnlocked(cfg, driver)
}

func createRegistryManifestUnlocked(cfg Config, driver DriverInfo) error {
	changes, err := registryRegistrationChanges(driver)
	if err != nil {
		return err
	}
	root := cfg.Level.key()
	driversKey, err := registry.OpenKey(root, regKeyADBC, registry.ALL_ACCESS)
	var adbcHandle *registryKeyHandle
	var driversHandle *registryKeyHandle
	var adbcCreated, driversCreated bool
	if err == nil {
		driversHandle = &registryKeyHandle{key: driversKey}
	} else if errors.Is(err, registry.ErrNotExist) {
		adbcKey, adbcOpenedExisting, createErr := registry.CreateKey(root, "SOFTWARE\\ADBC", registry.ALL_ACCESS)
		if createErr != nil {
			return createErr
		}
		adbcCreated = registryKeyWasCreated(adbcOpenedExisting)
		adbcHandle = &registryKeyHandle{key: adbcKey}
		defer adbcHandle.close()

		var driversOpenedExisting bool
		driversKey, driversOpenedExisting, createErr = registry.CreateKey(adbcKey, "Drivers", registry.ALL_ACCESS)
		if createErr != nil {
			return rollbackCreatedRegistryKeys(createErr, func() error {
				return deleteCreatedRegistryKey(root, "SOFTWARE\\ADBC", adbcHandle, adbcCreated)
			})
		}
		driversCreated = registryKeyWasCreated(driversOpenedExisting)
		driversHandle = &registryKeyHandle{key: driversKey}
	} else {
		return err
	}
	defer driversHandle.close()

	driverKey, driverOpenedExisting, err := registry.CreateKey(driversHandle.key, driver.ID, registry.ALL_ACCESS)
	if err != nil {
		var cleanup []func() error
		if adbcHandle != nil {
			cleanup = append(cleanup, func() error {
				return deleteCreatedRegistryKey(adbcHandle.key, "Drivers", driversHandle, driversCreated)
			})
		}
		if adbcCreated {
			cleanup = append(cleanup, func() error {
				return deleteCreatedRegistryKey(root, "SOFTWARE\\ADBC", adbcHandle, true)
			})
		}
		return rollbackCreatedRegistryKeys(err, cleanup...)
	}
	driverCreated := registryKeyWasCreated(driverOpenedExisting)
	driverHandle := &registryKeyHandle{key: driverKey}
	defer driverHandle.close()

	rollbackKey := func() error {
		if !driverCreated {
			return nil
		}
		cleanup := []func() error{
			func() error { return deleteCreatedRegistryKey(driversHandle.key, driver.ID, driverHandle, true) },
		}
		if adbcHandle != nil && driversCreated {
			cleanup = append(cleanup, func() error {
				return deleteCreatedRegistryKey(adbcHandle.key, "Drivers", driversHandle, true)
			})
		}
		if adbcCreated {
			cleanup = append(cleanup, func() error {
				return deleteCreatedRegistryKey(root, "SOFTWARE\\ADBC", adbcHandle, true)
			})
		}
		return cleanupRegistryKeys(cleanup...)
	}
	store := windowsRegistrationValueStore{key: driverHandle.key}
	if driverCreated {
		return updateRegistrationValuesWithRollback(store, changes, rollbackKey)
	}
	return updateRegistrationValues(store, changes)
}

type windowsRegistrationValueStore struct {
	key registry.Key
}

type registryKeyHandle struct {
	key     registry.Key
	closed  bool
	closeFn func() error
}

func (h *registryKeyHandle) close() error {
	if h == nil || h.closed {
		return nil
	}
	h.closed = true
	if h.closeFn != nil {
		return h.closeFn()
	}
	return h.key.Close()
}

func registryRegistrationChanges(driver DriverInfo) ([]registrationValueChange, error) {
	if driver.Version == nil {
		return nil, errors.New("driver version is required for registry registration")
	}
	values := []struct {
		name  string
		value string
	}{
		{name: "name", value: driver.Name},
		{name: "publisher", value: driver.Publisher},
		{name: "license", value: driver.License},
		{name: "version", value: driver.Version.String()},
		{name: "source", value: driver.Source},
		{name: "driver", value: driver.Driver.Shared.Get(PlatformTuple())},
	}
	changes := make([]registrationValueChange, 0, len(values)+2)
	for _, value := range values {
		encoded, err := registryStringValue(value.value)
		if err != nil {
			return nil, fmt.Errorf("encode registry value %q: %w", value.name, err)
		}
		changes = append(changes, registrationValueChange{name: value.name, value: encoded})
	}
	changes = append(changes, registrationValueChange{
		name: "manifest_version", value: registryDWordValue(currentManifestVersion),
	})
	entrypoint := registrationValue{}
	if driver.Driver.Entrypoint != "" {
		var err error
		entrypoint, err = registryStringValue(driver.Driver.Entrypoint)
		if err != nil {
			return nil, fmt.Errorf("encode registry value %q: %w", "entrypoint", err)
		}
	}
	changes = append(changes, registrationValueChange{name: "entrypoint", value: entrypoint})
	return changes, nil
}

func (s windowsRegistrationValueStore) read(name string) (registrationValue, error) {
	size, kind, err := s.key.GetValue(name, nil)
	if errors.Is(err, registry.ErrNotExist) {
		return registrationValue{}, nil
	}
	if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
		return registrationValue{}, err
	}
	data := make([]byte, size)
	actualKind := kind
	if size > 0 {
		n, readKind, err := s.key.GetValue(name, data)
		if err != nil {
			return registrationValue{}, err
		}
		if n < 0 || n > len(data) {
			return registrationValue{}, fmt.Errorf("registry value %q changed size while being read", name)
		}
		data = data[:n]
		actualKind = readKind
	}
	return registrationValue{exists: true, kind: actualKind, data: data}, nil
}

func (s windowsRegistrationValueStore) write(name string, value registrationValue) error {
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var dataPointer *byte
	if len(value.data) > 0 {
		dataPointer = &value.data[0]
	}
	result, _, _ := windows.NewLazySystemDLL("advapi32.dll").NewProc("RegSetValueExW").Call(
		uintptr(s.key), uintptr(unsafe.Pointer(namePointer)), 0, uintptr(value.kind),
		uintptr(unsafe.Pointer(dataPointer)), uintptr(len(value.data)),
	)
	if result != 0 {
		return windows.Errno(result)
	}
	return nil
}

func (s windowsRegistrationValueStore) delete(name string) error {
	err := s.key.DeleteValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func registryStringValue(value string) (registrationValue, error) {
	encoded, err := windows.UTF16FromString(value)
	if err != nil {
		return registrationValue{}, fmt.Errorf("invalid registry string value: %w", err)
	}
	data := make([]byte, len(encoded)*2)
	for i, unit := range encoded {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return registrationValue{exists: true, kind: registry.SZ, data: data}, nil
}

func registryDWordValue(value uint32) registrationValue {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, value)
	return registrationValue{exists: true, kind: registry.DWORD, data: data}
}

func deleteCreatedRegistryKey(parent registry.Key, name string, key *registryKeyHandle, created bool) error {
	if !created {
		return nil
	}
	closeErr := key.close()
	deleteErr := registry.DeleteKey(parent, name)
	return errors.Join(closeErr, deleteErr)
}
