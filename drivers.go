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
	_ "embed"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/internal"
	"github.com/go-faster/yaml"
	"github.com/google/uuid"
	machineid "github.com/zeroshade/machine-id"
)

var (
	ErrUnauthorized         = errors.New("not authorized")
	ErrUnauthorizedColumnar = errors.New("not authorized to access")
)

type Registry struct {
	Name    string
	Drivers []Driver
	BaseURL *url.URL
}

func mustParseURL(u string) *url.URL {
	uri, err := url.Parse(u)
	if err != nil {
		panic(fmt.Sprintf("failed to parse URL %s: %v", u, err))
	}
	return uri
}

var (
	registries = []Registry{
		{BaseURL: mustParseURL("https://dbc-cdn.columnar.tech")},
		{BaseURL: mustParseURL("https://dbc-cdn-private.columnar.tech")},
	}
	Version = "unknown"
	mid     string
	uid     uuid.UUID

	// use this default client for all requests,
	// it will add the dbc user-agent to all requests
	DefaultClient = http.DefaultClient
)

type uaRoundTripper struct {
	http.RoundTripper
	userAgent string
}

// custom RoundTripper that sets the User-Agent header on any requests
func (u *uaRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", u.userAgent)
	return u.RoundTripper.RoundTrip(req)
}

// SetProxy configures the HTTP client to use the specified proxy server.
// If proxy is empty, it uses the default transport (which may still respect HTTP_PROXY env var).
func SetProxy(proxy string) error {
	var transport http.RoundTripper = http.DefaultTransport
	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return fmt.Errorf("invalid proxy URL: %w", err)
		}
		transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	}

	// Preserve the user agent from the current transport
	var userAgent string
	if ua, ok := DefaultClient.Transport.(*uaRoundTripper); ok {
		userAgent = ua.userAgent
	} else {
		// Fallback, should not happen
		userAgent = "dbc-cli"
	}

	DefaultClient.Transport = &uaRoundTripper{
		RoundTripper: transport,
		userAgent:    userAgent,
	}
	return nil
}

func init() {
	info, ok := debug.ReadBuildInfo()
	if ok {
		Version = info.Main.Version
	}

	if val := os.Getenv("DBC_BASE_URL"); val != "" {
		registries = []Registry{
			{BaseURL: mustParseURL(val)},
		}
	}

	userAgent := fmt.Sprintf("dbc-cli/%s (%s; %s)",
		Version, runtime.GOOS, runtime.GOARCH)

	// many CI systems set CI=true in the env so let's check for that
	if ci := os.Getenv("CI"); ci != "" {
		if val, _ := strconv.ParseBool(ci); val {
			userAgent += " CI"
		}
	}

	DefaultClient.Transport = &uaRoundTripper{
		RoundTripper: http.DefaultTransport,
		userAgent:    userAgent,
	}

	mid, _ = machineid.ProtectedID()

	// get user config dir
	userdir, err := internal.GetUserConfigPath()
	if err != nil {
		// if we can't get the dir for some reason, just generate a new UUID
		uid = uuid.New()
		return
	}

	fp := filepath.Join(userdir, "uid.uuid")
	data, err := os.ReadFile(fp)
	if err == nil {
		if err = uid.UnmarshalBinary(data); err == nil {
			return
		}
	}

	// if the file didn't exist or we couldn't parse it, generate a new uuid
	// and then write a new file
	uid = uuid.New()
	// if we fail to create the dir or write the file, just ignore the error
	// and use the fresh UUID
	if err = os.MkdirAll(filepath.Dir(fp), 0o700); err == nil {
		if data, err = uid.MarshalBinary(); err == nil {
			os.WriteFile(fp, data, 0o600)
		}
	}
}

func makereq(u string) (resp *http.Response, err error) {
	uri, err := url.Parse(u)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL %s: %w", uri, err)
	}

	cred, err := auth.GetCredentials(uri)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	q := uri.Query()
	q.Add("mid", mid)
	q.Add("uid", uid.String())
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
			// if we're accessing the private registry then attempt to
			// fetch the trial license. This will be a no-op if they have
			// a license saved already, and if they haven't started their
			// trial or it is expired, then this will silently fail.
			_ = auth.FetchColumnarLicense(cred)
		}
		req.Header.Set("Authorization", "Bearer "+cred.GetAuthToken())
	}

	resp, err = DefaultClient.Do(&req)
	if err != nil {
		return
	}

	if resp.StatusCode == http.StatusUnauthorized && cred != nil {
		resp.Body.Close()
		// Try refreshing the token
		if !cred.Refresh() {
			return nil, fmt.Errorf("failed to refresh auth token")
		}

		req.Header.Set("Authorization", "Bearer "+cred.GetAuthToken())
		resp, err = DefaultClient.Do(&req)
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

	return resp, err
}

func getDriverListFromIndex(index *Registry) ([]Driver, error) {
	resp, err := makereq(index.BaseURL.JoinPath("/index.yaml").String())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch drivers: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// ignore registries we aren't authorized to access
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to fetch drivers: %s", resp.Status)
	}

	defer resp.Body.Close()
	drivers := struct {
		Name    string   `yaml:"name"`
		Drivers []Driver `yaml:"drivers"`
	}{}

	err = yaml.NewDecoder(resp.Body).Decode(&drivers)
	if err != nil {
		return nil, fmt.Errorf("failed to parse driver registry index: %s", err)
	}

	if drivers.Name != "" {
		index.Name = drivers.Name
	}

	// Set registry reference
	for i := range drivers.Drivers {
		drivers.Drivers[i].Registry = index
	}

	result := drivers.Drivers
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})

	return result, nil
}

var getDrivers = sync.OnceValues(func() ([]Driver, error) {
	allDrivers := make([]Driver, 0)
	for i := range registries {
		drivers, err := getDriverListFromIndex(&registries[i])
		if err != nil {
			return nil, err
		}
		registries[i].Drivers = drivers
		allDrivers = append(allDrivers, drivers...)
	}

	return allDrivers, nil
})

//go:embed columnar.pubkey
var armoredPubKey string

var getVerifier = sync.OnceValues(func() (crypto.PGPVerify, error) {
	key, err := crypto.NewKeyFromArmored(armoredPubKey)
	if err != nil {
		return nil, err
	}

	return crypto.PGP().Verify().VerificationKey(key).New()
})

type ProgressFunc func(written, total int64)

type progressWriter struct {
	w       io.Writer
	total   int64
	written int64
	fn      ProgressFunc
}

func (pw *progressWriter) Write(p []byte) (n int, err error) {
	n, err = pw.w.Write(p)
	pw.written += int64(n)
	if pw.fn != nil {
		pw.fn(pw.written, pw.total)
	}
	return
}

type PkgInfo struct {
	Driver        Driver
	Version       *semver.Version
	PlatformTuple string

	Path *url.URL
}

func (p PkgInfo) DownloadPackage(prog ProgressFunc) (*os.File, error) {
	if p.Path == nil {
		return nil, fmt.Errorf("cannot download package for %s: no url set", p.Driver.Title)
	}

	location := p.Path.String()
	rsp, err := makereq(location)
	if err != nil {
		return nil, fmt.Errorf("failed to download driver: %w", err)
	}

	if rsp.StatusCode != http.StatusOK {
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

	pw := &progressWriter{
		w:     output,
		total: rsp.ContentLength,
		fn:    prog,
	}

	_, err = io.Copy(pw, rsp.Body)
	if err != nil {
		output.Close()
	}
	return output, err
}

type pkginfo struct {
	Version  *semver.Version `yaml:"version"`
	Packages []struct {
		PlatformTuple string `yaml:"platform"`
		URL           string `yaml:"url"`
	} `yaml:"packages"`
}

func (p pkginfo) GetPackage(d Driver, platformTuple string) (PkgInfo, error) {
	if len(p.Packages) == 0 {
		return PkgInfo{}, fmt.Errorf("no packages available for version %s", p.Version)
	}

	base := d.Registry.BaseURL
	for _, pkg := range p.Packages {
		if pkg.PlatformTuple == platformTuple {
			var uri *url.URL

			if pkg.URL != "" {
				uri, _ = url.Parse(pkg.URL)
				if !uri.IsAbs() {
					uri = base.JoinPath(pkg.URL)
				}
			} else {
				uri = base.JoinPath(d.Path, p.Version.String(),
					d.Path+"_"+platformTuple+"-"+p.Version.String()+".tar.gz")
			}

			return PkgInfo{
				Driver:        d,
				Version:       p.Version,
				PlatformTuple: platformTuple,
				Path:          uri,
			}, nil
		}
	}

	return PkgInfo{}, fmt.Errorf("no package found for platform '%s'", platformTuple)
}

func filter[T any](items iter.Seq[T], predicate func(T) bool) iter.Seq[T] {
	return func(yield func(T) bool) {
		for item := range items {
			if predicate(item) && !yield(item) {
				return
			}
		}
	}
}

type Driver struct {
	Registry *Registry `yaml:"-"`

	Title   string    `yaml:"name"`
	Desc    string    `yaml:"description"`
	License string    `yaml:"license"`
	Path    string    `yaml:"path"`
	URLs    []string  `yaml:"urls"`
	DocsUrl string    `yaml:"docs_url"`
	PkgInfo []pkginfo `yaml:"pkginfo"`
}

func (d Driver) GetWithConstraint(c *semver.Constraints, platformTuple string) (PkgInfo, error) {
	if len(d.PkgInfo) == 0 {
		return PkgInfo{}, fmt.Errorf("no package info available for driver %s", d.Path)
	}

	itr := filter(slices.Values(d.PkgInfo), func(p pkginfo) bool {
		if !c.Check(p.Version) {
			return false
		}

		return slices.ContainsFunc(p.Packages, func(p struct {
			PlatformTuple string `yaml:"platform"`
			URL           string `yaml:"url"`
		}) bool {
			return p.PlatformTuple == platformTuple
		})
	})

	var result *pkginfo
	for pkg := range itr {
		if result == nil || pkg.Version.GreaterThan(result.Version) {
			result = &pkg
		}
	}

	if result == nil {
		return PkgInfo{}, fmt.Errorf("no package found for driver %s that satisfies constraints %s", d.Path, c)
	}

	return result.GetPackage(d, platformTuple)
}

func (d Driver) Versions(platformTuple string) semver.Collection {
	versions := make(semver.Collection, 0, len(d.PkgInfo))
	for _, pkg := range d.PkgInfo {
		for _, p := range pkg.Packages {
			if p.PlatformTuple == platformTuple {
				versions = append(versions, pkg.Version)
			}
		}
	}

	sort.Sort(versions)
	return versions
}

func (d Driver) GetPackage(version *semver.Version, platformTuple string) (PkgInfo, error) {
	var pkg pkginfo
	if version == nil {
		pkg = slices.MaxFunc(d.PkgInfo, func(a, b pkginfo) int {
			return a.Version.Compare(b.Version)
		})
		version = pkg.Version
	} else {
		idx := slices.IndexFunc(d.PkgInfo, func(p pkginfo) bool {
			return p.Version.Equal(version)
		})
		if idx == -1 {
			return PkgInfo{}, fmt.Errorf("version %s not found", version)
		}
		pkg = d.PkgInfo[idx]
	}

	return pkg.GetPackage(d, platformTuple)
}

func (d Driver) MaxVersion() pkginfo {
	return slices.MaxFunc(d.PkgInfo, func(a, b pkginfo) int {
		return a.Version.Compare(b.Version)
	})
}

func GetDriverList() ([]Driver, error) {
	return getDrivers()
}

// SignedByColumnar returns nil if the library was signed by
// the columnar public key (embedded in the CLI) or an error
// otherwise.
func SignedByColumnar(lib, sig io.Reader) error {
	verifier, err := getVerifier()
	if err != nil {
		return err
	}

	reader, err := verifier.VerifyingReader(lib, sig, crypto.Auto)
	if err != nil {
		return err
	}

	result, err := reader.DiscardAllAndVerifySignature()
	if err != nil {
		return err
	}

	return result.SignatureError()
}
