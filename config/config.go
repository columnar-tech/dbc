// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const adbcEnvVar = "ADBC_DRIVER_PATH"

var platformTuple string

func init() {
	os := runtime.GOOS
	switch os {
	case "darwin":
		os = "macos"
	case "windows", "freebsd", "linux", "openbsd":
	default:
		os = "unknown"
	}

	arch := runtime.GOARCH
	switch arch {
	case "386":
		arch = "x86"
	case "ppc":
		arch = "powerpc"
	case "ppc64":
		arch = "powerpc64"
	case "ppc64le":
		arch = "powerpc64le"
	case "wasm":
		arch = "wasm64"
	default:
	}

	platformTuple = os + "_" + arch
}

func PlatformTuple() string {
	return platformTuple
}

type Config struct {
	Level    ConfigLevel
	Location string
	Drivers  map[string]DriverInfo
	Exists   bool
	Err      error
}

type ConfigLevel int

const (
	ConfigUnknown ConfigLevel = iota
	ConfigSystem
	ConfigUser
	ConfigEnv
)

func (c ConfigLevel) String() string {
	switch c {
	case ConfigSystem:
		return "system"
	case ConfigUser:
		return "user"
	case ConfigEnv:
		return "env"
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

func EnsureLocation(cfg Config) (string, error) {
	loc := cfg.Location
	if cfg.Level == ConfigEnv {
		list := filepath.SplitList(loc)
		if len(list) == 0 {
			return "", errors.New("ADBC_DRIVER_PATH is empty, must be set to valid path to use")
		}
		loc = list[0]
	}

	if _, err := os.Stat(loc); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(loc, 0o755); err != nil {
				return "", fmt.Errorf("failed to create config directory %s: %w", loc, err)
			}
		} else {
			return "", fmt.Errorf("failed to stat config directory %s: %w", loc, err)
		}
	}

	return loc, nil
}

func loadDir(dir string) (map[string]DriverInfo, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}

	ret := make(map[string]DriverInfo)

	fsys := os.DirFS(dir)
	matches, _ := fs.Glob(fsys, "*.toml")
	for _, m := range matches {
		p := filepath.Join(dir, m)
		di, err := loadDriverFromManifest(filepath.Dir(p), filepath.Base(p))
		if err != nil {
			continue
		}

		di.FilePath = filepath.Dir(p)
		ret[di.ID] = di
	}
	return ret, nil
}

func loadConfig(lvl ConfigLevel) Config {
	cfg := Config{Level: lvl, Location: lvl.configLocation()}
	if cfg.Location == "" {
		return cfg
	}

	if lvl == ConfigEnv {
		pathList := filepath.SplitList(cfg.Location)
		slices.Reverse(pathList)
		finalDrivers := make(map[string]DriverInfo)
		for _, p := range pathList {
			drivers, err := loadDir(p)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				cfg.Err = fmt.Errorf("error loading drivers from %s: %w", p, err)
				return cfg
			}
			maps.Copy(finalDrivers, drivers)
		}
		cfg.Exists, cfg.Drivers = len(finalDrivers) > 0, finalDrivers
	}

	drivers, err := loadDir(cfg.Location)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			cfg.Err = fmt.Errorf("error loading drivers from %s: %w", cfg.Location, err)
		}
		return cfg
	}

	cfg.Exists, cfg.Drivers = true, drivers
	return cfg
}

func getEnvConfigDir() string {
	envConfigLoc := filepath.SplitList(os.Getenv(adbcEnvVar))
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		envConfigLoc = append(envConfigLoc, filepath.Join(venv, "etc", "adbc"))
	}

	if conda := os.Getenv("CONDA_PREFIX"); conda != "" {
		envConfigLoc = append(envConfigLoc, filepath.Join(conda, "etc", "adbc"))
	}

	envConfigLoc = slices.DeleteFunc(envConfigLoc, func(s string) bool {
		return s == ""
	})

	return strings.Join(envConfigLoc, string(filepath.ListSeparator))
}

func InstallDriver(cfg Config, shortName string, downloaded *os.File) (Manifest, error) {
	var (
		loc string
		err error
	)
	if loc, err = EnsureLocation(cfg); err != nil {
		return Manifest{}, fmt.Errorf("could not ensure config location: %w", err)
	}
	base := strings.TrimSuffix(path.Base(downloaded.Name()), ".tar.gz")
	finalDir := filepath.Join(loc, base)

	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("failed to create driver directory %s: %w", finalDir, err)
	}

	manifest, err := InflateTarball(downloaded, finalDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to extract tarball: %w", err)
	}

	driverPath := filepath.Join(finalDir, manifest.Files.Driver)

	manifest.DriverInfo.ID = shortName
	manifest.DriverInfo.Source = "dbc"
	manifest.DriverInfo.Driver.Shared.Set(PlatformTuple(), driverPath)

	return manifest, nil
}

// TODO: Unexport once we refactor sync.go. sync.go has it's own separate
// installation routine which it probably shouldn't.
func InflateTarball(f *os.File, outDir string) (Manifest, error) {
	defer f.Close()
	var m Manifest

	f.Seek(0, io.SeekStart)
	rdr, err := gzip.NewReader(f)
	if err != nil {
		return m, fmt.Errorf("could not create gzip reader: %w", err)
	}
	defer rdr.Close()

	t := tar.NewReader(rdr)
	for {
		hdr, err := t.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return m, fmt.Errorf("error reading tarball: %w", err)
		}

		if hdr.Name != "MANIFEST" {
			next, err := os.Create(filepath.Join(outDir, hdr.Name))
			if err != nil {
				return m, fmt.Errorf("could not create file %s: %w", hdr.Name, err)
			}

			if _, err = io.Copy(next, t); err != nil {
				next.Close()
				return m, fmt.Errorf("could not write file from tarball %s: %w", hdr.Name, err)
			}
			next.Close()
		} else {
			if err := toml.NewDecoder(t).Decode(&m); err != nil {
				return m, fmt.Errorf("could not decode manifest: %w", err)
			}
		}
	}

	return m, nil
}
