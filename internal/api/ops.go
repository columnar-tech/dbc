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

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/config"
)

// Target selects where Install, Uninstall and ListInstalled operate: exactly
// one of Location (a driver directory) or Level ("user" or "system", the same
// locations as `dbc install -l`).
type Target struct {
	Level    string `json:"level,omitempty"`
	Location string `json:"location,omitempty"`
}

// Config resolves t to the dbc configuration it names, using the same
// resolution as the CLI for levels.
func (t Target) Config() (config.Config, error) {
	switch {
	case t.Level != "" && t.Location != "":
		return config.Config{}, errors.New("set either level or location, not both")
	case t.Location != "":
		return config.Config{Level: config.ConfigEnv, Location: t.Location}, nil
	case t.Level == "":
		return config.Config{}, errors.New("either level or location is required")
	}
	var lvl config.ConfigLevel
	if err := lvl.UnmarshalText([]byte(t.Level)); err != nil {
		return config.Config{}, err
	}
	cfg, ok := config.Get()[lvl]
	if !ok {
		return config.Config{}, fmt.Errorf("%s config level is unavailable: no user configuration directory", lvl)
	}
	if cfg.Err != nil {
		return config.Config{}, fmt.Errorf("failed to load %s config: %w", lvl, cfg.Err)
	}
	return cfg, nil
}

type Driver struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	License     string `json:"license"`
	Description string `json:"description"`
}

type SearchResult struct {
	Drivers []Driver `json:"drivers"`
	// Warning is set when some registries failed but others answered, so the
	// result is partial rather than an error.
	Warning string `json:"warning,omitempty"`
}

type Latest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

type ResolveResult struct {
	Path     string   `json:"path"`
	Platform string   `json:"platform"`
	Versions []string `json:"versions"`
	Latest   *Latest  `json:"latest,omitempty"`
}

type Manifest struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Source     string `json:"source"`
	DriverPath string `json:"driverPath"`
}

type InstalledDriver struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	FilePath string `json:"filePath"`
}

func Search(ctx context.Context, c *dbc.Client, pattern string) (SearchResult, error) {
	drivers, searchErr := c.Search(ctx, pattern)
	if searchErr != nil && allRegistriesFailed(c, searchErr) {
		return SearchResult{}, searchErr
	}
	out := SearchResult{Drivers: make([]Driver, 0, len(drivers))}
	for _, d := range drivers {
		out.Drivers = append(out.Drivers, Driver{Path: d.Path, Title: d.Title, License: d.License, Description: d.Desc})
	}
	if searchErr != nil {
		out.Warning = searchErr.Error()
	}
	return out, nil
}

// allRegistriesFailed reports whether err, as returned by dbc.Client.Search,
// holds a failure for every configured registry.
func allRegistriesFailed(c *dbc.Client, err error) bool {
	failed := 1
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		failed = len(j.Unwrap())
	}
	return failed >= len(c.Registries())
}

// Resolve reports the versions of driver name available for platform and the
// newest stable package. An empty platform means the host platform.
func Resolve(ctx context.Context, c *dbc.Client, name, platform string) (ResolveResult, error) {
	if platform == "" {
		platform = config.PlatformTuple()
	}
	drivers, searchErr := c.Search(ctx, name)
	if searchErr != nil && allRegistriesFailed(c, searchErr) {
		return ResolveResult{}, searchErr
	}
	for _, d := range drivers {
		if d.Path != name {
			continue
		}
		out := ResolveResult{Path: d.Path, Platform: platform, Versions: []string{}}
		for _, v := range d.Versions(platform) {
			out.Versions = append(out.Versions, v.String())
		}
		// Same selection as Client.Install and `dbc install`, so latest is
		// never a version install would not fetch.
		if pkg, err := d.GetPackage(nil, platform, false); err == nil && pkg.Path != nil {
			out.Latest = &Latest{Version: pkg.Version.String(), URL: pkg.Path.String()}
		}
		return out, nil
	}
	if searchErr != nil {
		return ResolveResult{}, fmt.Errorf("driver %q not found in reachable registries; some registries failed: %w", name, searchErr)
	}
	return ResolveResult{}, fmt.Errorf("driver %q not found", name)
}

// InstallOptions adjusts Install. The zero value verifies signatures.
type InstallOptions struct {
	// AllowUnsigned skips signature verification, like the CLI's --no-verify.
	AllowUnsigned bool `json:"allowUnsigned"`
}

// Install installs driver name at t, verifying its signature unless
// opts.AllowUnsigned is set.
func Install(ctx context.Context, c *dbc.Client, name string, t Target, opts InstallOptions) (Manifest, error) {
	cfg, err := t.Config()
	if err != nil {
		return Manifest{}, err
	}
	var installOpts []dbc.InstallOption
	if opts.AllowUnsigned {
		installOpts = append(installOpts, dbc.AllowUnsigned())
	}
	m, err := c.Install(ctx, cfg, name, installOpts...)
	if err != nil {
		return Manifest{}, err
	}
	out := Manifest{
		ID:         m.DriverInfo.ID,
		Name:       m.DriverInfo.Name,
		Source:     m.DriverInfo.Source,
		DriverPath: m.DriverInfo.Driver.Shared.Get(config.PlatformTuple()),
	}
	if m.DriverInfo.Version != nil {
		out.Version = m.DriverInfo.Version.String()
	}
	return out, nil
}

// Uninstall removes driver name from t. It makes no network requests.
func Uninstall(name string, t Target) error {
	cfg, err := t.Config()
	if err != nil {
		return err
	}
	c, err := dbc.NewClient()
	if err != nil {
		return err
	}
	return c.Uninstall(cfg, name)
}

// ListInstalled returns the drivers installed at t, sorted by ID.
func ListInstalled(t Target) ([]InstalledDriver, error) {
	cfg, err := t.Config()
	if err != nil {
		return nil, err
	}
	var drivers []config.DriverInfo
	if cfg.Level == config.ConfigEnv {
		drivers = config.FindDriverConfigsIn(cfg.Location)
	} else {
		drivers = slices.Collect(maps.Values(cfg.Drivers))
	}
	out := make([]InstalledDriver, 0, len(drivers))
	for _, d := range drivers {
		e := InstalledDriver{ID: d.ID, Name: d.Name, FilePath: d.FilePath}
		if d.Version != nil {
			e.Version = d.Version.String()
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b InstalledDriver) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// Verify checks sig as a detached signature of lib by Columnar's signing key.
func Verify(lib, sig []byte) error {
	return dbc.SignedByColumnar(bytes.NewReader(lib), bytes.NewReader(sig))
}

// Login stores a token credential, replacing any credential for the same
// registry host. RegistryURL and AuthURI must be absolute URLs with nonempty
// hosts, and Token must be nonempty. RefreshToken and ClientID are optional.
// It does not perform an interactive authorization flow.
func Login(cred Credential) error {
	c, err := tokenCredential(cred)
	if err != nil {
		return err
	}
	if c.RegistryURL.Scheme == "" || c.RegistryURL.Host == "" {
		return errors.New("invalid credential registryURL: must be an absolute URL with a host")
	}
	if c.AuthURI.Scheme == "" || c.AuthURI.Host == "" {
		return errors.New("invalid credential authURI: must be an absolute URL with a host")
	}
	if c.Token == "" {
		return errors.New("credential token must not be empty")
	}
	return auth.AddCredential(c, true)
}

// Logout removes stored credentials for the registry host. It returns an
// error when registryURL is not an absolute URL with a nonempty host or when
// no credential for that host is stored.
func Logout(registryURL string) error {
	u, err := url.Parse(registryURL)
	if err != nil {
		return fmt.Errorf("invalid registryURL: %w", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return errors.New("invalid registryURL: must be an absolute URL with a host")
	}
	return auth.RemoveCredential(auth.Uri(*u))
}

// LicensePath returns the license file path beside the credential store.
// The file need not exist.
func LicensePath() (string, error) {
	return auth.LicensePath()
}

// InstallLicense copies srcPath into the credential directory. Without force,
// the source must be named columnar.lic and an existing license is preserved.
func InstallLicense(srcPath string, force bool) error {
	err := auth.InstallLicenseFromFile(srcPath, force)
	switch {
	case errors.Is(err, auth.ErrLicenseWrongFilename):
		return errors.New("source file is not named columnar.lic (set force to override)")
	case errors.Is(err, auth.ErrLicenseAlreadyExists):
		return errors.New("license already exists (set force to overwrite)")
	default:
		return err
	}
}
