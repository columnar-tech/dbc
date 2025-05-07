// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package dbc

import (
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"sync"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/goccy/go-yaml"
	"golang.org/x/mod/semver"
)

const baseURL = "http://localhost:8000"

var getDrivers = sync.OnceValues(func() ([]Driver, error) {
	resp, err := http.Get(baseURL + "/manifest.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch drivers: %s", resp.Status)
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
	Driver   Driver
	Version  string
	Platform string

	Path *url.URL
}

func (p PkgInfo) DownloadPackage() (*os.File, error) {
	if p.Path == nil {
		return nil, fmt.Errorf("cannot download package for %s: no url set", p.Driver.Title)
	}

	location := p.Path.String()
	rsp, err := http.Get(location)
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

type Driver struct {
	Title     string   `yaml:"name"`
	Desc      string   `yaml:"description"`
	License   string   `yaml:"license"`
	Path      string   `yaml:"path"`
	URLs      []string `yaml:"urls"`
	Versions  []string `yaml:"versions"`
	Platforms []string `yaml:"platforms"`
}

func (d Driver) GetPackage(version, platformTuple string) PkgInfo {
	if version == "" {
		version = slices.MaxFunc(d.Versions, semver.Compare)
	}

	pkg, _ := url.JoinPath(baseURL, d.Path, version[1:], d.Path+"_"+
		platformTuple+"-"+version[1:]+".tar.gz")
	uri, _ := url.Parse(pkg)
	return PkgInfo{
		Driver:   d,
		Version:  version,
		Platform: platformTuple,
		Path:     uri,
	}
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
