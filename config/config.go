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
		envConfigLoc = append(envConfigLoc, filepath.Join(venv, "etc", "adbc", "drivers"))
	}

	if conda := os.Getenv("CONDA_PREFIX"); conda != "" {
		envConfigLoc = append(envConfigLoc, filepath.Join(conda, "etc", "adbc", "drivers"))
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
	base := strings.TrimSuffix(filepath.Base(downloaded.Name()), ".tar.gz")
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
			m, err = decodeManifest(t, "", false)
			if err != nil {
				return m, fmt.Errorf("could not decode manifest: %w", err)
			}

		}
	}

	return m, nil
}

func decodeManifest(r io.Reader, driverName string, requireShared bool) (Manifest, error) {
	var di tomlDriverInfo
	if err := toml.NewDecoder(r).Decode(&di); err != nil {
		return Manifest{}, fmt.Errorf("error decoding manifest: %w", err)
	}

	if di.ManifestVersion > currentManifestVersion {
		return Manifest{}, fmt.Errorf("manifest version %d is unsupported, only %d and lower are supported by this version of dbc",
			di.ManifestVersion, currentManifestVersion)
	}

	result := Manifest{
		DriverInfo: DriverInfo{
			ID:        driverName,
			Name:      di.Name,
			Publisher: di.Publisher,
			License:   di.License,
			Version:   di.Version,
			Source:    di.Source,
			AdbcInfo:  di.AdbcInfo,
		},
		Files:       di.Files,
		PostInstall: di.PostInstall,
	}

	result.Driver.Entrypoint = di.Driver.Entrypoint
	switch s := di.Driver.Shared.(type) {
	case string:
		result.Driver.Shared.defaultPath = s
	case map[string]any:
		result.Driver.Shared.platformMap = make(map[string]string)
		for k, v := range s {
			if strVal, ok := v.(string); ok {
				result.Driver.Shared.platformMap[k] = strVal
			} else {
				return Manifest{}, fmt.Errorf("invalid type for platform %s, expected string", k)
			}
		}
	default:
		if requireShared {
			return Manifest{}, errors.New("invalid type for 'Driver.shared' in manifest, expected string or table")
		}
	}

	return result, nil
}

// Common, non-platform-specific code for uninstalling a driver. Called by
// platform-specific UninstallDriver function.
func UninstallDriverShared(cfg Config, info DriverInfo) error {
	for sharedPath := range info.Driver.Shared.Paths() {
		// Run filepath.Clean on sharedPath mainly to catch inner ".." in the path
		sharedPath = filepath.Clean(sharedPath)

		// Don't remove anything that isn't contained withing the found driver's
		// config directory (i.e., avoid malicious driver manifests)
		if !strings.HasPrefix(sharedPath, cfg.Location) {
			continue
		}

		// dbc installs drivers in a folder, other tools may not so we handle each
		// differently.
		if info.Source == "dbc" {
			sharedDir := filepath.Dir(sharedPath)
			// Edge case when manifest is ill-formed: if sharedPath is set to the
			// folder containing the shared library instead of the shared library
			// itself, sharedDir is cfg.Location and we definitely don't want to
			// remove that
			if sharedDir == cfg.Location {
				continue
			}

			if err := os.RemoveAll(sharedDir); err != nil {
				// Ignore only when not found. This supports manifest-only drivers.
				// TODO: Come up with a better mechanism to handle manifest-only drivers
				// and remove this continue when we do
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return fmt.Errorf("error removing driver %s: %w", info.ID, err)
			}
		} else {
			if err := os.Remove(sharedPath); err != nil {
				// Ignore only when not found. This supports manifest-only drivers.
				// TODO: Come up with a better mechanism to handle manifest-only drivers
				// and remove this continue when we do
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return fmt.Errorf("error removing driver %s: %w", info.ID, err)
			}
		}
	}

	// Manifest only drivers can come with extra files such as a LICENSE and we
	// create a folder next to the driver manifest to store them, same as we'd
	// store the actual driver shared library. Above, we find the path of this
	// folder by looking at the Driver.shared path. For manifest-only drivers,
	// Driver.shared is not a valid path (it's just a name), so this trick doesn't
	// work. We do want to clean this folder up so here we guess what it is and
	// try to remove it e.g., "somedriver_macos_arm64_v1.2.3."
	extra_folder := fmt.Sprintf("%s_%s_v%s", info.ID, platformTuple, info.Version)
	extra_folder = filepath.Clean(extra_folder)
	extra_path := filepath.Join(cfg.Location, extra_folder)
	finfo, err := os.Stat(extra_path)
	if err == nil && finfo.IsDir() && extra_path != "." {
		_ = os.RemoveAll(extra_path)
		// ignore errors
	}

	return nil
}
