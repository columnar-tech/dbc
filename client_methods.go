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

package dbc

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/config"
	"github.com/go-faster/yaml"
)

func (c *Client) makeRequest(u string) (*http.Response, error) {
	c.setup()

	uri, err := url.Parse(u)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL %s: %w", u, err)
	}

	cred, err := c.getCredential(uri)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	q := uri.Query()
	q.Add("mid", c.mid)
	q.Add("uid", c.uid.String())
	uri.RawQuery = q.Encode()

	req := http.Request{
		Method: http.MethodGet,
		URL:    uri,
		Header: http.Header{},
	}

	if uri.Path == "/index.yaml" {
		req.Header.Set("Accept", "application/yaml")
	}

	if cred != nil {
		if auth.IsColumnarPrivateRegistry(uri) {
			_ = auth.FetchColumnarLicense(cred)
		}
		req.Header.Set("Authorization", "Bearer "+cred.GetAuthToken())
	}

	resp, err := c.httpClient.Do(&req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized && cred != nil {
		resp.Body.Close()
		if !cred.Refresh() {
			return nil, fmt.Errorf("failed to refresh auth token")
		}
		req.Header.Set("Authorization", "Bearer "+cred.GetAuthToken())
		resp, err = c.httpClient.Do(&req)
		if err != nil {
			return nil, err
		}
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		err = ErrUnauthorized
		if auth.IsColumnarPrivateRegistry(uri) && cred != nil {
			err = ErrUnauthorizedColumnar
		}
		resp.Body.Close()
		return nil, fmt.Errorf("%s%s: %w", uri.Host, uri.Path, err)
	}

	return resp, nil
}

func (c *Client) getDriverListFromIndex(index *Registry) ([]Driver, error) {
	resp, err := c.makeRequest(index.BaseURL.JoinPath("/index.yaml").String())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch drivers: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("failed to fetch drivers: %s", resp.Status)
	}

	defer resp.Body.Close()
	drivers := struct {
		Name    string   `yaml:"name"`
		Drivers []Driver `yaml:"drivers"`
	}{}

	if err = yaml.NewDecoder(resp.Body).Decode(&drivers); err != nil {
		return nil, fmt.Errorf("failed to parse driver registry index: %s", err)
	}

	if drivers.Name != "" {
		index.Name = drivers.Name
	}

	for i := range drivers.Drivers {
		drivers.Drivers[i].Registry = index
	}

	result := drivers.Drivers
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})

	return result, nil
}

// Search searches for drivers matching the given pattern across all registries.
func (c *Client) Search(pattern string) ([]Driver, error) {
	var (
		allDrivers []Driver
		totalErr   error
	)

	for i := range c.registries {
		drivers, err := c.getDriverListFromIndex(&c.registries[i])
		if err != nil {
			totalErr = errors.Join(totalErr, fmt.Errorf("registry %s: %w", c.registries[i].BaseURL, err))
			continue
		}
		c.registries[i].Drivers = drivers
		allDrivers = append(allDrivers, drivers...)
	}

	if pattern == "" {
		return allDrivers, totalErr
	}

	lowerPattern := strings.ToLower(pattern)
	var filtered []Driver
	for _, d := range allDrivers {
		if strings.Contains(strings.ToLower(d.Path), lowerPattern) ||
			strings.Contains(strings.ToLower(d.Title), lowerPattern) {
			filtered = append(filtered, d)
		}
	}

	return filtered, totalErr
}

func (c *Client) downloadPackage(pkg PkgInfo) (*os.File, error) {
	if pkg.Path == nil {
		return nil, fmt.Errorf("cannot download package for %s: no url set", pkg.Driver.Title)
	}

	location := pkg.Path.String()
	rsp, err := c.makeRequest(location)
	if err != nil {
		return nil, fmt.Errorf("failed to download driver: %w", err)
	}

	if rsp.StatusCode != http.StatusOK {
		rsp.Body.Close()
		return nil, fmt.Errorf("failed to download driver %s: %s", location, rsp.Status)
	}
	defer rsp.Body.Close()

	fname := path.Base(location)
	tmpdir, err := os.MkdirTemp(os.TempDir(), "adbc-drivers-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	output, err := os.Create(path.Join(tmpdir, fname))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file to download to: %w", err)
	}

	if _, err = io.Copy(output, rsp.Body); err != nil {
		output.Close()
		return nil, fmt.Errorf("failed to write driver file: %w", err)
	}

	return output, nil
}

// Install installs a driver with the given name to the specified configuration.
func (c *Client) Install(cfg config.Config, driverName string) (*config.Manifest, error) {
	drivers, err := c.Search(driverName)
	// Only fail if the driver wasn't found in any registry; partial registry errors
	// are acceptable as long as we can still locate the target driver.
	if err != nil && len(drivers) == 0 {
		return nil, fmt.Errorf("failed to search for driver %s: %w", driverName, err)
	}

	var found *Driver
	for i := range drivers {
		if drivers[i].Path == driverName {
			found = &drivers[i]
			break
		}
	}

	if found == nil {
		return nil, fmt.Errorf("driver %q not found", driverName)
	}

	pkg, err := found.GetPackage(nil, config.PlatformTuple(), false)
	if err != nil {
		return nil, fmt.Errorf("failed to get package for driver %s: %w", driverName, err)
	}

	f, err := c.downloadPackage(pkg)
	if err != nil {
		return nil, fmt.Errorf("failed to download driver %s: %w", driverName, err)
	}
	defer func() { f.Close(); os.RemoveAll(filepath.Dir(f.Name())) }()

	manifest, err := config.InstallDriver(cfg, driverName, f)
	if err != nil {
		return nil, fmt.Errorf("failed to install driver %s: %w", driverName, err)
	}

	if err := config.CreateManifest(cfg, manifest.DriverInfo); err != nil {
		return nil, fmt.Errorf("failed to create manifest for driver %s: %w", driverName, err)
	}

	return &manifest, nil
}

// Uninstall uninstalls a driver with the given name from the specified configuration.
func (c *Client) Uninstall(cfg config.Config, driverName string) error {
	di, err := config.GetDriver(cfg, driverName)
	if err != nil {
		return fmt.Errorf("failed to find driver %q: %w", driverName, err)
	}

	if err := config.UninstallDriver(cfg, di); err != nil {
		return fmt.Errorf("failed to uninstall driver %q: %w", driverName, err)
	}

	return nil
}
