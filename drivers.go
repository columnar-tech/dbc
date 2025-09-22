// Copyright (c) 2025 Columnar Technologies Inc.  All rights reserved.

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
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/go-faster/yaml"
	"github.com/google/uuid"
	machineid "github.com/zeroshade/machine-id"
)

const defaultURL = "https://dbc-cdn.columnar.tech"

var (
	baseURL   = defaultURL
	Version   = "unknown"
	userAgent string
	mid       string
	uid       uuid.UUID
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if ok {
		Version = info.Main.Version
	}

	if val := os.Getenv("DBC_BASE_URL"); val != "" {
		baseURL = val
	}

	userAgent = fmt.Sprintf("dbc-cli/%s (%s; %s)",
		Version, runtime.GOOS, runtime.GOARCH)

	// many CI systems set CI=true in the env so let's check for that
	if ci := os.Getenv("CI"); ci != "" {
		if val, _ := strconv.ParseBool(ci); val {
			userAgent += " CI"
		}
	}

	mid, _ = machineid.ProtectedID()

	// get user config dir
	userdir, err := os.UserConfigDir()
	if err != nil {
		// if we can't get the dir for some reason, just generate a new UUID
		uid = uuid.New()
		return
	}

	// try to read the existing UUID file
	dirname := "columnar"
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		dirname = "Columnar"
	}

	fp := filepath.Join(userdir, dirname, "dbc", "uid.uuid")
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

	q := uri.Query()
	q.Add("mid", mid)
	q.Add("uid", uid.String())
	uri.RawQuery = q.Encode()

	req := http.Request{
		Method: http.MethodGet,
		URL:    uri,
		Header: http.Header{
			"User-Agent": []string{userAgent},
		},
	}

	return http.DefaultClient.Do(&req)
}

var getDrivers = sync.OnceValues(func() ([]Driver, error) {
	resp, err := makereq(baseURL + "/index.yaml")
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
		return nil, fmt.Errorf("failed to parse driver index: %s", err)
	}

	// Sort by path (short name)
	result := drivers.Drivers
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})

	return result, nil
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
