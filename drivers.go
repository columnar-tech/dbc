// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package dbc

import (
	_ "embed"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/goccy/go-yaml"
)

const baseURL = "https://dbc-cdn.columnar.tech"

var version = "unknown"

func init() {
	info, ok := debug.ReadBuildInfo()
	if ok {
		version = info.Main.Version
	}
}

func makereq(u string) (resp *http.Response, err error) {
	uri, err := url.Parse(u)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL %s: %w", uri, err)
	}

	req := http.Request{
		Method: http.MethodGet,
		URL:    uri,
		Header: http.Header{
			"User-Agent": []string{fmt.Sprintf("dbc-cli/%s (%s; %s)",
				version, runtime.GOOS, runtime.GOARCH)},
		},
	}

	return http.DefaultClient.Do(&req)
}

var getDrivers = sync.OnceValues(func() ([]Driver, error) {
	resp, err := makereq(baseURL + "/manifest.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch drivers: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch drivers: %s", resp.Status)
	}

	defer resp.Body.Close()
	drivers := struct {
		Drivers []Driver `yaml:"drivers"`
	}{}

	err = yaml.NewDecoder(resp.Body).Decode(&drivers)
	if err != nil {
		return nil, fmt.Errorf("failed to parse driver manifest: %s", err)
	}

	return drivers.Drivers, nil
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

type PkgInfo struct {
	Driver        Driver
	Version       *semver.Version
	PlatformTuple string

	Path *url.URL
}

func (p PkgInfo) DownloadPackage() (*os.File, error) {
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

	_, err = io.Copy(output, rsp.Body)
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

	base, _ := url.Parse(baseURL)
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
	Title   string    `yaml:"name"`
	Desc    string    `yaml:"description"`
	License string    `yaml:"license"`
	Path    string    `yaml:"path"`
	URLs    []string  `yaml:"urls"`
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
