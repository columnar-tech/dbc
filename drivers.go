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
	"crypto/sha256"
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
	"strings"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/internal"
	"github.com/columnar-tech/dbc/internal/registryurl"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/go-faster/yaml"
	"github.com/google/uuid"
)

var (
	ErrUnauthorized         = errors.New("not authorized")
	ErrUnauthorizedColumnar = errors.New("not authorized to access")
)

type Registry struct {
	Name    string
	Drivers []Driver
	BaseURL *url.URL
	// Signing says which key the registry's driver packages are verified
	// with. The zero value is the built-in Columnar key.
	Signing SigningPolicy
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
			CheckRedirect: checkRedirect,
		}

		mid, _ = telemetryMachineID()

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
	uri, err := url.Parse(u)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL %s: %w", u, err)
	}

	// Replaced requests are anonymous: no credentials, no telemetry ids.
	router := defaultRouter.Load()
	eff, replaced, err := router.route(uri)
	if err != nil {
		return nil, err
	}
	if replaced {
		return anonymousGet(context.Background(), getHTTPClient(), router, eff, uri.Path)
	}

	ensureSetup()
	// A redirect must not reach an origin that a rule replaces either.
	hc := router.guardRedirects(getHTTPClient(), false)

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
			_ = auth.FetchColumnarLicense(context.Background(), cred)
		}
		token = cred.GetAuthToken(context.Background())
	}

	req, err := buildLegacyReq(token)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	resp, err = hc.Do(req)
	if err != nil {
		return
	}

	if resp.StatusCode == http.StatusUnauthorized && cred != nil {
		resp.Body.Close()
		if err := cred.RefreshAndPersist(context.Background()); err != nil {
			return nil, fmt.Errorf("failed to refresh auth token: %w", err)
		}
		retryReq, retryErr := buildLegacyReq(cred.GetAuthToken(context.Background()))
		if retryErr != nil {
			return nil, fmt.Errorf("failed to build retry request: %w", retryErr)
		}
		resp, err = hc.Do(retryReq)
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

	// ArchiveSHA256 is the lowercase hex SHA-256 (64 characters, without the
	// "sha256:" prefix) of the package archive as published by the registry.
	// It is empty when the registry did not publish a hash for this package.
	ArchiveSHA256 string
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

	// Name the file after the escaped URL path only: a query (for example a
	// registry token) must not end up in the file name, and decoding could
	// turn %5C into a Windows path separator.
	fname := path.Base(p.Path.EscapedPath())
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

const registryHashPrefix = "sha256:"

// registryHash is the optional "hash" of a registry package entry. Decoding
// never fails: a malformed value is only recorded, so that just the affected
// package is rejected when it is resolved instead of the whole index.
type registryHash struct {
	digest  string
	present bool
	valid   bool
}

func (h *registryHash) UnmarshalYAML(value *yaml.Node) error {
	h.present = true
	h.valid = false
	if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
		return nil
	}
	hexDigest, ok := strings.CutPrefix(value.Value, registryHashPrefix)
	if !ok || len(hexDigest) != sha256.Size*2 {
		return nil
	}
	for _, c := range hexDigest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil
		}
	}
	h.digest = hexDigest
	h.valid = true
	return nil
}

type pkgEntry struct {
	PlatformTuple string       `yaml:"platform"`
	URL           string       `yaml:"url"`
	Hash          registryHash `yaml:"hash"`
}

type pkginfo struct {
	Version  *semver.Version `yaml:"version"`
	Packages []pkgEntry      `yaml:"packages"`
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
			if pkg.Hash.present && !pkg.Hash.valid {
				return PkgInfo{}, fmt.Errorf("invalid archive hash in registry for driver %s version %s platform %s: expected %s followed by 64 lowercase hex characters",
					d.Path, p.Version, platformTuple, registryHashPrefix)
			}
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

				ArchiveSHA256: pkg.Hash.digest,
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

// Driver is a single driver in a registry and its available versions.
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

func (d Driver) validatePackageVersions() error {
	for _, pkg := range d.PkgInfo {
		// TODO: we may want to do other validation
		if pkg.Version == nil {
			return fmt.Errorf("invalid package metadata for driver %s: missing version", d.Path)
		}
	}
	return nil
}

// GetWithConstraint returns the newest compatible package satisfying c.
func (d Driver) GetWithConstraint(c *semver.Constraints, platformTuple string) (PkgInfo, error) {
	return d.latestPackage(c, platformTuple, false)
}

// GetLatestPackage returns the newest compatible package for the given platform.
func (d Driver) GetLatestPackage(platform string, allowPrerelease bool) (PkgInfo, error) {
	return d.latestPackage(nil, platform, allowPrerelease)
}

// GetLatestVersionAnyPlatform returns the newest eligible version regardless of platform.
// When constraint is supplied, it determines prerelease eligibility, regardless of allowPrerelease.
func (d Driver) GetLatestVersionAnyPlatform(constraint *semver.Constraints, allowPrerelease bool) (*semver.Version, error) {
	if err := d.validatePackageVersions(); err != nil {
		return nil, err
	}
	var latest *semver.Version
	for pkg := range d.eligiblePackages(constraint, allowPrerelease) {
		if latest == nil || pkg.Version.GreaterThan(latest) {
			latest = pkg.Version
		}
	}
	if latest == nil {
		return nil, d.selectionMiss(constraint)
	}
	return latest, nil
}

func (d Driver) eligiblePackages(constraint *semver.Constraints, allowPrerelease bool) iter.Seq[pkginfo] {
	return filter(slices.Values(d.PkgInfo), func(pkg pkginfo) bool {
		return resolution.VersionEligible(pkg.Version, constraint, allowPrerelease)
	})
}

func (d Driver) selectionMiss(constraint *semver.Constraints) error {
	switch {
	case constraint != nil && len(d.PkgInfo) == 0:
		return packageMiss{fmt.Errorf("no package info available for driver %s", d.Path)}
	case constraint != nil:
		return packageMiss{fmt.Errorf("no package found for driver %s that satisfies constraints %s", d.Path, constraint)}
	case len(d.PkgInfo) > 0:
		// TODO: it may also be possible there's no packages for this platform?
		return packageMiss{fmt.Errorf("driver `%s` not found (but prerelease versions filtered out); try: dbc install --pre %s", d.Path, d.Path)}
	default:
		return packageMiss{fmt.Errorf("driver `%s` not found", d.Path)}
	}
}

func (d Driver) latestPackage(constraint *semver.Constraints, platform string, allowPrerelease bool) (PkgInfo, error) {
	if err := d.validatePackageVersions(); err != nil {
		return PkgInfo{}, err
	}
	var latest pkginfo
	var eligible bool
	for pkg := range d.eligiblePackages(constraint, allowPrerelease) {
		eligible = true
		if latest.Version != nil && !pkg.Version.GreaterThan(latest.Version) {
			continue
		}
		if slices.ContainsFunc(pkg.Packages, func(entry pkgEntry) bool {
			return entry.PlatformTuple == platform
		}) {
			latest = pkg
		}
	}
	if !eligible {
		return PkgInfo{}, d.selectionMiss(constraint)
	}
	if latest.Version == nil {
		return PkgInfo{}, fmt.Errorf("no package found for platform '%s'", platform)
	}
	return latest.GetPackage(d, platform)
}

func (d Driver) Versions(platformTuple string) semver.Collection {
	versions := make(semver.Collection, 0, len(d.PkgInfo))
	for _, pkg := range d.PkgInfo {
		if pkg.Version == nil {
			continue
		}
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
	if err := d.validatePackageVersions(); err != nil {
		return PkgInfo{}, err
	}
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
				return PkgInfo{}, packageMiss{fmt.Errorf("driver `%s` not found (but prerelease versions filtered out); try: dbc install --pre %s", d.Path, d.Path)}
			}
			return PkgInfo{}, packageMiss{fmt.Errorf("driver `%s` not found", d.Path)}
		}
	}

	var pkg pkginfo
	if version == nil {
		if len(pkglist) == 0 {
			return PkgInfo{}, packageMiss{fmt.Errorf("driver `%s` not found", d.Path)}
		}
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
				return PkgInfo{}, packageMiss{fmt.Errorf("version %s is a prerelease; use --pre to allow it", version)}
			}
			return PkgInfo{}, packageMiss{fmt.Errorf("version %s not found", version)}
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
	return c.Search(context.Background(), "")
}

// Deprecated: Signature verification is now handled internally by Client.Install.
// SignedByColumnar returns nil if the library was signed by
// the columnar public key (embedded in the CLI) or an error
// otherwise.
func SignedByColumnar(lib, sig io.Reader) error {
	key, err := ColumnarSigningKey()
	if err != nil {
		return err
	}
	return key.Verify(lib, sig)
}

// ErrPackageMiss indicates that a registry entry has no eligible version.
var ErrPackageMiss = errors.New("no eligible package version")

// packageMiss marks a selection miss that permits registry fallback.
type packageMiss struct{ error }

func (e packageMiss) Is(target error) bool { return target == ErrPackageMiss }

// RegistryIndexError reports that the index of one registry could not be
// fetched or parsed. Client.Search joins one per failed registry.
type RegistryIndexError struct {
	// Registry is the configuration of the registry, which also tells how
	// its packages are verified.
	Registry *Registry
	Err      error
}

// Error names the registry without credentials. The cause is redacted too:
// an HTTP error repeats the request URL with the registry's user information
// and query. Unwrap still returns the original cause.
func (e *RegistryIndexError) Error() string {
	msg := fmt.Sprintf("registry %s: %v", registryLabel(e.Registry), e.Err)
	if e.Registry == nil {
		return msg
	}
	return registryurl.Redact(msg, registryurl.Secrets([]*url.URL{e.Registry.BaseURL}))
}

func (e *RegistryIndexError) Unwrap() error { return e.Err }

// ErrIncompleteIndex is returned by SigningOperation.ResolvePackage
// when the index of a registry is missing and that registry might list the
// driver with another signer than the registries that do list it. Choosing where a driver comes
// from compares the signers of every registry that lists the driver, so a
// missing index only matters when its registry's signer differs (or cannot
// be determined).
var ErrIncompleteIndex = errors.New("the index of a registry that might list the driver with another signer is missing")

// SignerConflictError is returned by SigningOperation.ResolvePackage
// when registries whose packages are verified with different signers list
// the same driver name: registries with different certificates, or two
// unsigned registries. A fallback from one registry to another is only
// allowed between registries with the same signer, so the registry has to
// be chosen explicitly.
type SignerConflictError struct {
	Name string
	// Registries are the registries that list the driver, in priority order.
	Registries []*Registry
	// Signers describe the signers of Registries, in the same order.
	Signers []string
}

func (e *SignerConflictError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "driver `%s` is published by registries whose packages are verified with different signers:", e.Name)
	for i, r := range e.Registries {
		fmt.Fprintf(&b, " %s (%s)", registryLabel(r), e.Signers[i])
		if i < len(e.Registries)-1 {
			b.WriteString(",")
		}
	}
	return b.String()
}

// registryLabel names a registry in messages without its user information,
// query or fragment, which may hold credentials.
func registryLabel(r *Registry) string {
	if r == nil || r.BaseURL == nil {
		return "unknown registry"
	}
	u := *r.BaseURL
	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
	if r.Name != "" {
		return fmt.Sprintf("%s [%s]", u.String(), r.Name)
	}
	return u.String()
}

// resolvePackage implements SigningOperation.ResolvePackage with
// signerOf as the source of signer identities.
func resolvePackage(drivers []Driver, indexErr error, name string, selectPackage func(*Driver) (PkgInfo, error), signerOf func(*Registry) (signerIdentity, error)) (PkgInfo, error) {
	var (
		candidates []*Registry
		listed     []Driver // the first entry of each candidate
	)
	for _, d := range drivers {
		if d.Path == name && !slices.Contains(candidates, d.Registry) {
			candidates = append(candidates, d.Registry)
			listed = append(listed, d)
		}
	}
	// A single registry without failed ones needs no comparison, so its key
	// is not read here.
	if len(candidates) > 1 || indexErr != nil {
		signers := make([]signerIdentity, 0, len(candidates))
		for i, r := range candidates {
			signer, err := signerOf(r)
			if err != nil {
				return PkgInfo{Driver: listed[i]}, fmt.Errorf("driver %s: %s: %w", name, registryLabel(r), err)
			}
			signers = append(signers, signer)
		}
		for _, s := range signers[min(1, len(signers)):] {
			if s.key != signers[0].key {
				labels := make([]string, len(signers))
				for i, s := range signers {
					labels[i] = s.label
				}
				return PkgInfo{}, &SignerConflictError{Name: name, Registries: candidates, Signers: labels}
			}
		}
		if indexErr != nil {
			if err := checkFailedRegistries(indexErr, signers, signerOf); err != nil {
				return PkgInfo{}, fmt.Errorf("cannot resolve driver %s: %w", name, err)
			}
		}
	}

	var first PkgInfo
	var firstErr error
	for _, d := range drivers {
		if d.Path != name {
			continue
		}
		pkg, err := selectPackage(&d)
		if err == nil {
			pkg.Driver = d
			return pkg, nil
		}
		if !errors.Is(err, ErrPackageMiss) {
			return PkgInfo{Driver: d}, err
		}
		if firstErr == nil {
			first = PkgInfo{Driver: d}
			firstErr = err
		}
	}
	if firstErr != nil {
		return first, firstErr
	}
	return PkgInfo{}, fmt.Errorf("driver `%s` not found in driver registry index; try: `dbc search` to list available drivers", name)
}

// checkFailedRegistries returns ErrIncompleteIndex, joined with indexErr,
// unless every failure in indexErr is a RegistryIndexError of a registry
// whose signer is the same as the candidates' signer (signers), or, without
// candidates, the same as that of the other failed registries.
func checkFailedRegistries(indexErr error, signers []signerIdentity, signerOf func(*Registry) (signerIdentity, error)) error {
	failed, ok := failedRegistries(indexErr)
	if !ok {
		return fmt.Errorf("%w:\n%w", ErrIncompleteIndex, indexErr)
	}
	var want string
	known := len(signers) > 0
	if known {
		want = signers[0].key
	}
	for _, r := range failed {
		signer, err := signerOf(r)
		if err != nil {
			return fmt.Errorf("%w: %s: %w\n%w", ErrIncompleteIndex, registryLabel(r), err, indexErr)
		}
		if !known {
			want, known = signer.key, true
		}
		if signer.key != want {
			return fmt.Errorf("%w:\n%w", ErrIncompleteIndex, indexErr)
		}
	}
	return nil
}

// failedRegistries returns the registries of the RegistryIndexErrors in the
// tree of err, and whether every failure in it is one with a registry. It
// does not look inside a RegistryIndexError: its cause is the failure of
// that one registry.
func failedRegistries(err error) ([]*Registry, bool) {
	switch e := err.(type) {
	case *RegistryIndexError:
		return []*Registry{e.Registry}, e.Registry != nil
	case interface{ Unwrap() []error }:
		var all []*Registry
		for _, sub := range e.Unwrap() {
			registries, ok := failedRegistries(sub)
			if !ok {
				return nil, false
			}
			all = append(all, registries...)
		}
		return all, len(all) > 0
	case interface{ Unwrap() error }:
		return failedRegistries(e.Unwrap())
	}
	return nil, false
}
