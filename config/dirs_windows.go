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
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/Masterminds/semver/v3"
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

func setKeyMust(k registry.Key, name, value string) {
	if err := k.SetStringValue(name, value); err != nil {
		panic(err)
	}
}

func setKeyIntMust(k registry.Key, name string, value uint32) {
	if err := k.SetDWordValue(name, value); err != nil {
		panic(err)
	}
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
	if cfg.Level == ConfigEnv {
		if cfg.Location == "" {
			return fmt.Errorf("cannot write manifest to env config without %s set", adbcEnvVar)
		}
		loc, err := EnsureLocation(cfg)
		if err != nil {
			return err
		}
		return createDriverManifest(loc, driver)
	}

	var k registry.Key

	if !cfg.Exists {
		k, _, err = registry.CreateKey(cfg.Level.key(), "SOFTWARE\\ADBC", registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer k.Close()

		k, _, err = registry.CreateKey(k, "Drivers", registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer k.Close()
	} else {
		k, err = registry.OpenKey(cfg.Level.key(), regKeyADBC, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer k.Close()
	}

	dkey, _, err := registry.CreateKey(k, driver.ID, registry.ALL_ACCESS)
	if err != nil {
		return err
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

	setKeyMust(dkey, "name", driver.Name)
	setKeyIntMust(dkey, "manifest_version", currentManifestVersion)
	setKeyMust(dkey, "publisher", driver.Publisher)
	setKeyMust(dkey, "license", driver.License)
	setKeyMust(dkey, "version", driver.Version.String())
	setKeyMust(dkey, "source", driver.Source)
	setKeyMust(dkey, "driver", driver.Driver.Shared.Get(PlatformTuple()))
	if driver.Driver.Entrypoint != "" {
		setKeyMust(dkey, "entrypoint", driver.Driver.Entrypoint)
	}
	return nil
}

func UninstallDriver(cfg Config, info DriverInfo) error {
	if err := UninstallDriverShared(info); err != nil {
		return fmt.Errorf("failed to delete driver shared object: %w", err)
	}

	if cfg.Level != ConfigEnv {
		k, err := registry.OpenKey(cfg.Level.key(), regKeyADBC, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer k.Close()

		if err := registry.DeleteKey(k, info.ID); err != nil {
			return fmt.Errorf("failed to delete driver registry key: %w", err)
		}
	} else {
		manifest := filepath.Join(info.FilePath, info.ID+".toml")
		if err := os.Remove(manifest); err != nil {
			return fmt.Errorf("error removing manifest %s: %w", manifest, err)
		}

		// TODO: Remove this when the driver managers are fixed (>=1.8.1).
		removeManifestSymlink(info.FilePath, info.ID)
	}

	return nil
}
