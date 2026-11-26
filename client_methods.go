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
	"context"
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

func (c *Client) makeRequest(ctx context.Context, u string) (*http.Response, error) {
	uri, err := url.Parse(u)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL %s: %w", u, err)
	}

	// Replaced requests are anonymous: no credentials, no telemetry ids.
	eff, replaced, err := c.router.route(uri)
	if err != nil {
		return nil, err
	}
	if replaced {
		return anonymousGet(ctx, c.httpClient, c.router, eff, uri.Path)
	}

	c.setup()
	// A redirect must not reach an origin that a rule replaces either.
	hc := c.router.guardRedirects(c.httpClient, false)

	cred, err := c.getCredential(uri)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	q := uri.Query()
	q.Add("mid", c.mid)
	q.Add("uid", c.uid.String())
	uri.RawQuery = q.Encode()

	buildReq := func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), nil)
		if err != nil {
			return nil, err
		}
		if uri.Path == "/index.yaml" {
			req.Header.Set("Accept", "application/yaml")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return req, nil
	}

	token := ""
	if cred != nil {
		if auth.IsColumnarPrivateRegistry(uri) {
			_ = auth.FetchColumnarLicense(ctx, cred)
		}
		token = cred.GetAuthToken(ctx)
	}

	req, err := buildReq(token)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized && cred != nil {
		resp.Body.Close()
		if err := cred.RefreshAndPersist(ctx); err != nil {
			return nil, fmt.Errorf("failed to refresh auth token: %w", err)
		}
		req, err = buildReq(cred.GetAuthToken(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to build retry request: %w", err)
		}
		resp, err = hc.Do(req)
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

func (c *Client) getDriverListFromIndex(ctx context.Context, index *Registry) ([]Driver, error) {
	resp, err := c.makeRequest(ctx, index.BaseURL.JoinPath("/index.yaml").String())
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

	// A name from the registry configuration takes precedence; the index
	// only names registries that the configuration leaves unnamed.
	if index.Name == "" {
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
func (c *Client) Search(ctx context.Context, pattern string) ([]Driver, error) {
	var (
		allDrivers []Driver
		errs       []error
	)

	for i := range c.registries {
		drivers, err := c.getDriverListFromIndex(ctx, &c.registries[i])
		if err != nil {
			errs = append(errs, &RegistryIndexError{Registry: &c.registries[i], Err: err})
			continue
		}
		c.registries[i].Drivers = drivers
		allDrivers = append(allDrivers, drivers...)
	}
	// One joined error per failed registry.
	totalErr := errors.Join(errs...)

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

func (c *Client) downloadPackage(ctx context.Context, pkg PkgInfo) (*os.File, error) {
	if pkg.Path == nil {
		return nil, fmt.Errorf("cannot download package for %s: no url set", pkg.Driver.Title)
	}

	location := pkg.Path.String()
	rsp, err := c.makeRequest(ctx, location)
	if err != nil {
		return nil, fmt.Errorf("failed to download driver: %w", err)
	}

	if rsp.StatusCode != http.StatusOK {
		rsp.Body.Close()
		return nil, fmt.Errorf("failed to download driver %s: %s", location, rsp.Status)
	}
	defer rsp.Body.Close()

	// Name the file after the escaped URL path only: a query (for example a
	// registry token) must not end up in the file name, and decoding could
	// turn %5C into a Windows path separator.
	fname := path.Base(pkg.Path.EscapedPath())
	tmpdir, err := os.MkdirTemp(os.TempDir(), "adbc-drivers-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	var output *os.File
	defer func() {
		if output == nil {
			os.RemoveAll(tmpdir)
		}
	}()

	output, err = os.Create(path.Join(tmpdir, fname))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file to download to: %w", err)
	}

	if _, err = io.Copy(output, rsp.Body); err != nil {
		output.Close()
		output = nil
		return nil, fmt.Errorf("failed to write driver file: %w", err)
	}

	return output, nil
}

// Download fetches the tarball for pkg and returns its contents as an
// io.ReadCloser. The caller is responsible for closing the returned body.
// Auth credentials are resolved and injected automatically, including token
// refresh on 401.
func (c *Client) Download(ctx context.Context, pkg PkgInfo) (io.ReadCloser, error) {
	if pkg.Path == nil {
		return nil, fmt.Errorf("cannot download package for %s: no url set", pkg.Driver.Title)
	}
	rsp, err := c.makeRequest(ctx, pkg.Path.String())
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", pkg.Path, err)
	}
	if rsp.StatusCode != http.StatusOK {
		defer rsp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(rsp.Body, 1024))
		if len(body) > 0 {
			return nil, fmt.Errorf("failed to download %s: %s: %s", pkg.Path, rsp.Status, body)
		}
		return nil, fmt.Errorf("failed to download %s: %s", pkg.Path, rsp.Status)
	}
	return rsp.Body, nil
}

// InstallOption configures Client.Install.
type InstallOption func(*installOptions)

type installOptions struct{ allowUnsigned bool }

// AllowUnsigned makes Client.Install skip signature verification, like the
// CLI's --no-verify. Use it only for drivers from a trusted private registry.
func AllowUnsigned() InstallOption {
	return func(o *installOptions) { o.allowUnsigned = true }
}

// Install installs a driver with the given name to the specified
// configuration, verifying the driver library's signature with the key that
// the registry of the selected package uses (the Columnar key unless the
// registry's Signing says otherwise) unless AllowUnsigned is given or the
// registry is Unsigned. When the registry publishes a SHA-256 for the
// package, the downloaded archive must match it even with AllowUnsigned.
// Verification runs on the staged package before it is
// published, so a driver that fails it leaves any existing install in place.
func (c *Client) Install(ctx context.Context, cfg config.Config, driverName string, opts ...InstallOption) (*config.Manifest, error) {
	var o installOptions
	for _, opt := range opts {
		opt(&o)
	}

	drivers, searchErr := c.Search(ctx, driverName)
	// Without any listed driver, the registry errors are the explanation.
	if searchErr != nil && len(drivers) == 0 {
		return nil, fmt.Errorf("failed to search for driver %s: %w", driverName, searchErr)
	}
	// The signer check and the verification below use the same keys. A
	// failed registry is an error only if it might list the driver with
	// another signer; see SigningOperation.ResolvePackage.
	var signing SigningOperation
	pkg, err := signing.ResolvePackage(drivers, searchErr, driverName, func(d *Driver) (PkgInfo, error) { return d.GetLatestPackage(config.PlatformTuple(), false) })
	if err != nil {
		return nil, fmt.Errorf("failed to get package for driver %s: %w", driverName, err)
	}

	installOpts := config.InstallPackageOptions{ExpectedArchiveSHA256: pkg.ArchiveSHA256, ExpectedVersion: pkg.Version}
	if !o.allowUnsigned {
		// The key is selected before the download, so a key that cannot be
		// loaded fails without fetching the package.
		key, err := signing.registryKey(pkg.Driver.Registry)
		if err != nil {
			return nil, fmt.Errorf("driver %s: could not load the signing key of its registry: %w", driverName, err)
		}
		if key != nil {
			installOpts.SignatureVerifier = key.VerifyPackage
		}
	}

	f, err := c.downloadPackage(ctx, pkg)
	if err != nil {
		return nil, fmt.Errorf("failed to download driver %s: %w", driverName, err)
	}
	defer os.RemoveAll(filepath.Dir(f.Name()))

	manifest, err := config.InstallPackage(ctx, cfg, driverName, f, installOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to install driver %s: %w", driverName, err)
	}

	return &manifest, nil
}

// Uninstall removes a driver's runtime registration from the specified
// configuration. For external registrations, the shared library remains in
// place because dbc does not own it.
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

// OpenAnonymous opens u with a GET request that carries no registry
// credentials and no telemetry identifiers, for a file that a driver source
// other than a registry names, such as an HTTP URL template source in
// dbc.toml. u must be an absolute http or https URL without user
// information, query or fragment. The client's URL replacements apply as for
// a registry, but a URL replacement or redirect never turns an HTTPS request
// into an HTTP one, and a redirect never reaches an origin that a rule
// replaces. In Wasm, the browser's fetch follows redirects internally, so
// these redirect checks cannot be applied there. A status other than 200 OK
// is an error. Errors name u, never a URL that a replacement or redirect led
// to, and unwrap to the cause. The caller closes the returned body.
func (c *Client) OpenAnonymous(ctx context.Context, u *url.URL) (io.ReadCloser, error) {
	if u == nil || (!strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https")) ||
		u.Host == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("open: an absolute http or https URL without user information, query or fragment is required")
	}
	where := u.Host + u.EscapedPath()
	eff, replaced, err := c.router.route(u)
	if err != nil {
		return nil, &openError{where: where, err: err}
	}
	if !replaced {
		eff = u
	}
	if strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(eff.Scheme, "https") {
		// newURLRouter rejects such rules; keep the guarantee here as well.
		return nil, &openError{where: where, err: errors.New("refusing to replace an https URL by an http one")}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eff.String(), nil)
	if err != nil {
		return nil, &openError{where: where, err: err}
	}
	resp, err := c.router.guardRedirects(noDowngrade(c.httpClient), replaced).Do(req)
	if err != nil {
		return nil, &openError{where: where, err: err}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &openError{where: where, status: resp.StatusCode}
	}
	return resp.Body, nil
}

// openError is a failure of OpenAnonymous. Its text names the requested
// location only: the *url.Error of net/http names the URL it last tried,
// which a redirect may have chosen, with a query that may hold a token.
type openError struct {
	where  string
	status int
	err    error
}

func (e *openError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("request to %s: %d %s", e.where, e.status, http.StatusText(e.status))
	}
	cause := e.err
	if urlErr, ok := errors.AsType[*url.Error](cause); ok {
		cause = urlErr.Err
	}
	return fmt.Sprintf("request to %s: %v", e.where, cause)
}

func (e *openError) Unwrap() error { return e.err }

// noDowngrade returns a copy of hc that also refuses HTTPS to HTTP redirects,
// whatever the redirect policy of hc, which may come from WithHTTPClient. The
// scheme of the previous request is read before that policy runs, since the
// policy may change the request it is given and the requests before it.
func noDowngrade(hc *http.Client) *http.Client {
	guarded := *hc
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		secure := strings.EqualFold(via[len(via)-1].URL.Scheme, "https")
		downgraded := func() bool { return secure && !strings.EqualFold(req.URL.Scheme, "https") }
		if downgraded() {
			return errors.New("refusing HTTPS to HTTP redirect")
		}
		if hc.CheckRedirect == nil {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		}
		if err := hc.CheckRedirect(req, via); err != nil {
			// Includes http.ErrUseLastResponse, which must reach net/http
			// unwrapped.
			return err
		}
		if downgraded() {
			return errors.New("refusing HTTPS to HTTP redirect")
		}
		return nil
	}
	return &guarded
}
