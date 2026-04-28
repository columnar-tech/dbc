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
	Version = "unknown"
	mid     string
	uid     uuid.UUID

	// DefaultClient is the HTTP client used for all requests.
	//
	// Deprecated: Use NewClient with WithHTTPClient instead.
	// DefaultClient must be set during program initialization,
	// before any concurrent calls to GetDriverList or makereq.
	DefaultClient = http.DefaultClient

	setupOnce      sync.Once
	internalClient *http.Client
)

type uaRoundTripper struct {
	http.RoundTripper
	userAgent string
}

// custom RoundTripper that sets the User-Agent header on any requests
func (u *uaRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", u.userAgent)
	return u.RoundTripper.RoundTrip(req)
}

func init() {
	info, ok := debug.ReadBuildInfo()
	if ok && Version == "unknown" {
		Version = info.Main.Version
	}
}

func ensureSetup() {
	setupOnce.Do(func() {
		userAgent := fmt.Sprintf("dbc-cli/%s (%s; %s)",
			Version, runtime.GOOS, runtime.GOARCH)

		if ci := os.Getenv("CI"); ci != "" {
			if val, _ := strconv.ParseBool(ci); val {
				userAgent += " CI"
			}
		}

		internalClient = &http.Client{
			Transport: &uaRoundTripper{
				RoundTripper: http.DefaultTransport,
				userAgent:    userAgent,
			},
		}

		mid, _ = machineid.ProtectedID()

		userdir, err := internal.GetUserConfigPath()
		if err != nil {
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

		uid = uuid.New()
		if err = os.MkdirAll(filepath.Dir(fp), 0o700); err == nil {
			if data, err = uid.MarshalBinary(); err == nil {
				os.WriteFile(fp, data, 0o600)
			}
		}
	})
}

func getHTTPClient() *http.Client {
	ensureSetup()
	if DefaultClient != http.DefaultClient {
		return DefaultClient
	}
	return internalClient
}

func makereq(u string) (resp *http.Response, err error) {
	ensureSetup()

	uri, err := url.Parse(u)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL %s: %w", u, err)
	}

	cred, err := auth.GetCredentials(uri)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	q := uri.Query()
	q.Add("mid", mid)
	q.Add("uid", uid.String())
	uri.RawQuery = q.Encode()

	buildLegacyReq := func(token string) (*http.Request, error) {
		urlCopy := *uri
		r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, urlCopy.String(), nil)
		if err != nil {
			return nil, err
		}
		if uri.Path == "/index.yaml" {
			r.Header.Set("Accept", "application/yaml")
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		return r, nil
	}

	token := ""
	if cred != nil {
		if auth.IsColumnarPrivateRegistry(uri) {
			// if we're accessing the private registry then attempt to
			// fetch the trial license. This will be a no-op if they have
			// a license saved already, and if they haven't started their
			// trial or it is expired, then this will silently fail.
			_ = auth.FetchColumnarLicense(cred)
		}
		token = cred.GetAuthToken()
	}

	req, err := buildLegacyReq(token)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	resp, err = getHTTPClient().Do(req)
	if err != nil {
		return
	}

	if resp.StatusCode == http.StatusUnauthorized && cred != nil {
		resp.Body.Close()
		if err := cred.Refresh(); err != nil {
			return nil, fmt.Errorf("failed to refresh auth token: %w", err)
		}
		retryReq, retryErr := buildLegacyReq(cred.GetAuthToken())
		if retryErr != nil {
			return nil, fmt.Errorf("failed to build retry request: %w", retryErr)
		}
		resp, err = getHTTPClient().Do(retryReq)
		if err != nil {
			return
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

	return resp, err
}

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

// Deprecated: Use Client.Download instead.
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
		os.RemoveAll(tmpdir)
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
		output = nil
		os.RemoveAll(tmpdir)
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

	if d.Registry == nil {
		return PkgInfo{}, fmt.Errorf("cannot resolve package URL for %s: driver has no registry", d.Title)
	}
	base := d.Registry.BaseURL
	for _, pkg := range p.Packages {
		if pkg.PlatformTuple == platformTuple {
			var uri *url.URL

			if pkg.URL != "" {
				var err error
				uri, err = url.Parse(pkg.URL)
				if err != nil {
					return PkgInfo{}, fmt.Errorf("invalid package URL %q: %w", pkg.URL, err)
				}
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
	DocsURL string    `yaml:"docs_url"`
	PkgInfo []pkginfo `yaml:"pkginfo"`
}

func (d Driver) HasNonPrerelease() bool {
	return slices.ContainsFunc(d.PkgInfo, func(p pkginfo) bool {
		return p.Version.Prerelease() == ""
	})
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
			found := pkg
			result = &found
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

func (d Driver) GetPackage(version *semver.Version, platformTuple string, allowPrerelease bool) (PkgInfo, error) {
	pkglist := d.PkgInfo

	// Filter prereleases when no specific stable version is requested.
	// When version is a specific stable release (Prerelease() == ""),
	// filtering is unnecessary — the exact-match search below will
	// only match the requested stable version.
	if !allowPrerelease && (version == nil || version.Prerelease() != "") {
		hadPackages := len(d.PkgInfo) > 0
		pkglist = slices.Collect(filter(slices.Values(d.PkgInfo), func(p pkginfo) bool {
			return p.Version.Prerelease() == ""
		}))
		if len(pkglist) == 0 {
			if hadPackages {
				return PkgInfo{}, fmt.Errorf("driver `%s` not found (but prerelease versions filtered out); try: dbc install --pre %s", d.Path, d.Path)
			}
			return PkgInfo{}, fmt.Errorf("driver `%s` not found", d.Path)
		}
	}

	var pkg pkginfo
	if version == nil {
		pkg = slices.MaxFunc(pkglist, func(a, b pkginfo) int {
			return a.Version.Compare(b.Version)
		})
		version = pkg.Version
	} else {
		idx := slices.IndexFunc(pkglist, func(p pkginfo) bool {
			return p.Version.Equal(version)
		})
		if idx == -1 {
			if !allowPrerelease && version.Prerelease() != "" {
				return PkgInfo{}, fmt.Errorf("version %s is a prerelease; use --pre to allow it", version)
			}
			return PkgInfo{}, fmt.Errorf("version %s not found", version)
		}
		pkg = pkglist[idx]
	}

	return pkg.GetPackage(d, platformTuple)
}

func (d Driver) MaxVersion() (VersionInfo, bool) {
	if len(d.PkgInfo) == 0 {
		return VersionInfo{}, false
	}
	p := slices.MaxFunc(d.PkgInfo, func(a, b pkginfo) int {
		return a.Version.Compare(b.Version)
	})
	pkgs := make([]PackageInfo, 0, len(p.Packages))
	for _, pkg := range p.Packages {
		pkgs = append(pkgs, PackageInfo{
			Platform: pkg.PlatformTuple,
			URL:      pkg.URL,
		})
	}
	return VersionInfo{Version: p.Version, Packages: pkgs}, true
}

// PackageInfo holds the platform and raw URL string for a single package entry.
// The URL may be relative (joined against the registry base URL) or absolute.
type PackageInfo struct {
	Platform string
	URL      string
}

// VersionInfo holds the version and its associated packages for a driver.
type VersionInfo struct {
	Version  *semver.Version
	Packages []PackageInfo
}

// AllVersions returns all version/package entries for the driver as exported
// VersionInfo values. This allows callers outside the dbc package to iterate
// over every version and platform without needing access to the unexported
// pkginfo type.
func (d Driver) AllVersions() []VersionInfo {
	result := make([]VersionInfo, 0, len(d.PkgInfo))
	for _, pi := range d.PkgInfo {
		pkgs := make([]PackageInfo, 0, len(pi.Packages))
		for _, p := range pi.Packages {
			pkgs = append(pkgs, PackageInfo{
				Platform: p.PlatformTuple,
				URL:      p.URL,
			})
		}
		result = append(result, VersionInfo{
			Version:  pi.Version,
			Packages: pkgs,
		})
	}
	return result
}

// GetDriverList returns a list of all available drivers from all configured registries.
//
// Deprecated: Use NewClient and Client.Search instead.
func GetDriverList() ([]Driver, error) {
	ensureSetup()
	var opts []Option
	if val := os.Getenv("DBC_BASE_URL"); val != "" {
		opts = append(opts, WithBaseURL(val))
	}
	c, err := NewClient(append(opts, WithHTTPClient(getHTTPClient()))...)
	if err != nil {
		return nil, err
	}
	return c.Search("")
}

// Deprecated: Signature verification is now handled internally by Client.Install.
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

func GetLatestDbcVersion() (*semver.Version, error) {
	resp, err := makereq("https://dbc.columnar.tech")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest dbc version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("failed to fetch latest dbc version: %s", resp.Status)
	}

	return semver.NewVersion(resp.Header.Get("x-dbc-latest"))
}
