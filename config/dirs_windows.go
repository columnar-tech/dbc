// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	ConfigUnknown ConfigLevel = iota
	ConfigSystem
	ConfigUser
)

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

func (c ConfigLevel) driverLocation() string {
	var prefix string
	switch c {
	case ConfigSystem:
		prefix = "C:\\Program Files"
	case ConfigUser:
		prefix, _ = os.UserConfigDir()
	default:
		panic("unknown config level")
	}

	return filepath.Join(prefix, "ADBC", "drivers")
}

func (c ConfigLevel) String() string {
	switch c {
	case ConfigSystem:
		return "system"
	case ConfigUser:
		return "user"
	default:
		return "unknown"
	}
}

func (c *ConfigLevel) UnmarshalText(b []byte) error {
	switch strings.ToLower(strings.TrimSpace(string(b))) {
	case "system":
		*c = ConfigSystem
	case "user":
		*c = ConfigUser
	default:
		return errors.New("unknown config level")
	}
	return nil
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

func driverInfoFromKey(k registry.Key, driverName string) (di DriverInfo, err error) {
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

	di.ID = driverName
	di.Name = keyMust(dkey, "name")
	di.Publisher = keyOptional(dkey, "publisher")
	di.License = keyOptional(dkey, "license")
	di.Version = keyMust(dkey, "version")
	di.Source = keyOptional(dkey, "source")
	di.Driver.Shared = keyMust(dkey, "driver")

	return
}

func loadConfig(lvl ConfigLevel) Config {
	ret := Config{Level: lvl, Location: lvl.driverLocation()}
	k, err := registry.OpenKey(lvl.key(), regKeyADBC, registry.READ)
	if err != nil {
		return ret
	}
	defer k.Close()

	info, err := k.Stat()
	if err != nil {
		log.Println(err)
		return ret
	}

	ret.Exists, ret.Drivers = true, make(map[string]DriverInfo)
	if info.SubKeyCount == 0 {
		log.Println("No drivers found")
		return ret
	}

	drivers, err := k.ReadSubKeyNames(int(info.SubKeyCount))
	if err != nil {
		log.Println(err)
		return ret
	}

	for _, driver := range drivers {
		di, err := driverInfoFromKey(k, driver)
		if err != nil {
			log.Println(err)
			continue
		}
		ret.Drivers[driver] = di
	}

	return ret
}

func Get() map[ConfigLevel]Config {
	return map[ConfigLevel]Config{
		ConfigSystem: loadConfig(ConfigSystem),
		ConfigUser:   loadConfig(ConfigUser),
	}
}

func FindDriverConfigs(lvl ConfigLevel) []DriverInfo {
	return slices.Collect(maps.Values(loadConfig(lvl).Drivers))
}

func GetDriver(cfg Config, driverName string) (DriverInfo, error) {
	k, err := registry.OpenKey(cfg.Level.key(), regKeyADBC, registry.READ)
	if err != nil {
		return DriverInfo{}, err
	}
	defer k.Close()

	return driverInfoFromKey(k, driverName)
}

func CreateManifest(cfg Config, driver DriverInfo) (err error) {
	var k registry.Key

	if !cfg.Exists {
		k, _, err = registry.CreateKey(cfg.Level.key(), "SOFTWARE\\ADBC", registry.WRITE)
		if err != nil {
			return err
		}
		defer k.Close()

		k, _, err = registry.CreateKey(k, "Drivers", registry.WRITE)
		if err != nil {
			return err
		}
		defer k.Close()
	} else {
		k, err = registry.OpenKey(cfg.Level.key(), regKeyADBC, registry.WRITE)
		if err != nil {
			return err
		}
		defer k.Close()
	}

	dkey, _, err := registry.CreateKey(k, driver.ID, registry.WRITE)
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
	setKeyMust(dkey, "publisher", driver.Publisher)
	setKeyMust(dkey, "license", driver.License)
	setKeyMust(dkey, "version", driver.Version)
	setKeyMust(dkey, "source", driver.Source)
	setKeyMust(dkey, "driver", driver.Driver.Shared)
	return nil
}

func DeleteDriver(cfg Config, info DriverInfo) error {
	k, err := registry.OpenKey(cfg.Level.key(), regKeyADBC, registry.WRITE)
	if err != nil {
		return err
	}
	defer k.Close()

	if info.Source == "dbc" {
		if err := os.RemoveAll(filepath.Dir(info.Driver.Shared)); err != nil {
			return fmt.Errorf("failed to remove driver directory: %w", err)
		}
	} else {
		if err := os.Remove(info.Driver.Shared); err != nil {
			return fmt.Errorf("failed to remove driver: %w", err)
		}
	}

	if err := registry.DeleteKey(k, info.ID); err != nil {
		return fmt.Errorf("failed to delete driver registry key: %w", err)
	}

	return nil
}
