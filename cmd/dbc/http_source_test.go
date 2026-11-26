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

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseURLTemplate(t *testing.T) {
	for _, raw := range []string{
		"https://example.com/v{version}/drv_{os}_{arch}.tar.gz",
		"http://intranet:8080/drivers/drv.tar.gz",
	} {
		_, err := parseURLTemplate("url", raw)
		assert.NoError(t, err, raw)
	}
	for raw, want := range map[string]string{
		"example.com/drv.tar.gz":                 "absolute http or https",
		"ftp://example.com/drv.tar.gz":           "absolute http or https",
		"https://example.com":                    "must have a path",
		"https://{os}.example.com/drv.tar.gz":    "in its path only",
		"https://u:p@example.com/drv.tar.gz":     "user information",
		"https://example.com/drv.tar.gz?token=1": "a query",
		"https://example.com/drv.tar.gz#x":       "a fragment",
		"https://example.com/{platform}.tar.gz":  "{platform}",
		"https://example.com/{version.tar.gz":    "unmatched",
		"https://example.com/version}.tar.gz":    "unmatched",
	} {
		_, err := parseURLTemplate("url", raw)
		assert.ErrorContains(t, err, want, raw)
	}

	tmpl, err := parseURLTemplate("url", "https://example.com/v{version}/drv_{os}_{arch}.tar.gz")
	require.NoError(t, err)
	u, err := tmpl.expand(semver.MustParse("1.2.3+build.4"), resolution.Target{OS: "macos", Arch: "arm64"})
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/v1.2.3+build.4/drv_macos_arm64.tar.gz", u.String())
}

// The identity of an HTTP source is part of every lock file that locks
// one, so its encoding must never change.
func TestHTTPSourceIdentity(t *testing.T) {
	raw := "HTTPS://Example.COM/v{version}/drv_{os}_{arch}.tar.gz"
	src, err := driverSource{Type: sourceTypeHTTP, URL: &raw}.http()
	require.NoError(t, err)
	assert.Equal(t, "sha256:ce10937abf5df3db72f656f45a34f622939266a03ea6c6a3cf06b87e65d48787", src.identity(),
		"the digest of https://example.com/v{version}/drv_{os}_{arch}.tar.gz, with the scheme and host in lower case")
	upper := "https://example.com/V{version}/drv_{os}_{arch}.tar.gz"
	other, err := driverSource{Type: sourceTypeHTTP, URL: &upper}.http()
	require.NoError(t, err)
	assert.NotEqual(t, src.identity(), other.identity(), "the path is kept as written")

	identity := func(raw string) string {
		src, err := driverSource{Type: sourceTypeHTTP, URL: &raw}.http()
		require.NoError(t, err, raw)
		return src.identity()
	}
	assert.Equal(t, identity("http://[fe80::1%25en0]:8080/drv.tar.gz"), identity("HTTP://[FE80::1%25en0]:8080/drv.tar.gz"),
		"the case of an IPv6 address does not matter")
	assert.NotEqual(t, identity("http://[fe80::1%25en0]/drv.tar.gz"), identity("http://[fe80::1%25EN0]/drv.tar.gz"),
		"an IPv6 zone keeps its case")
	assert.Equal(t, identity("http://[fe80::1]/drv.tar.gz"), identity("http://[FE80::1]/drv.tar.gz"))

	checksum := "https://example.com/SHA256SUMS"
	withChecksum, err := driverSource{Type: sourceTypeHTTP, URL: &raw, ChecksumURL: &checksum, SigningKey: false}.http()
	require.NoError(t, err)
	assert.Equal(t, src.identity(), withChecksum.identity(), "neither checksum_url nor signing_key is part of the identity")
}

func TestHTTPSourceValidation(t *testing.T) {
	https, http := "https://example.com/drv.tar.gz", "http://example.com/SHA256SUMS"
	name := "acme"
	for want, src := range map[string]driverSource{
		"must have url":               {Type: sourceTypeHTTP},
		"has no name":                 {Type: sourceTypeHTTP, URL: &https, Name: &name},
		"checksum_url must use https": {Type: sourceTypeHTTP, URL: &https, ChecksumURL: &http},
		"signing_key = true":          {Type: sourceTypeHTTP, URL: &https, SigningKey: true},
		`"columnar" is not valid`:     {Type: sourceTypeHTTP, URL: &https, SigningKey: "columnar"},
		"not int64":                   {Type: sourceTypeHTTP, URL: &https, SigningKey: int64(1)},
	} {
		_, err := src.http()
		assert.ErrorContains(t, err, want)
	}
}

func TestExactHTTPVersion(t *testing.T) {
	for _, raw := range []string{"=1.2.3+build.4", "1.2.3+build.4"} {
		v, err := exactHTTPVersion(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, "1.2.3+build.4", v.String())
	}
	for _, raw := range []any{"1.2", "v1.2.3", ">=1.2.3", "==1.2.3", "=v1.2.3", "=1.2", "= 1.2.3", " 1.2.3", "=1.2.3 || =1.2.4", "latest", "", nil, 3} {
		_, err := exactHTTPVersion(raw)
		assert.Error(t, err, "%v", raw)
	}
}

func TestChecksumFor(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	archive, _ := url.Parse("https://example.com/v1/drv_linux_amd64.tar.gz")
	sums := a + "  drv_linux_amd64.tar.gz\n" + b + " *drv_macos_arm64.tar.gz\r\n\n"
	got, err := checksumFor([]byte(sums), archive, false)
	require.NoError(t, err)
	assert.Equal(t, a, got)

	got, err = checksumFor([]byte(strings.ToUpper(a)+"\n"), archive, true)
	require.NoError(t, err)
	assert.Equal(t, a, got, "a hash alone for one archive")

	for _, tc := range []struct{ want, body string }{
		{"does not list", b + "  other.tar.gz\n"},
		{"more than once", a + "  drv_linux_amd64.tar.gz\n" + a + "  drv_linux_amd64.tar.gz\n"},
		{"does not start with a SHA-256", "SHA256 (drv_linux_amd64.tar.gz) = " + a + "\n"},
		{"as sha256sum writes them", a + "\tdrv_linux_amd64.tar.gz\n"},
		{"as sha256sum writes them", a + " \n"},
		{"as sha256sum writes them", a + " drv_linux_amd64.tar.gz\n"},
		{"must hold one hash", a + "\n" + b + "\n"},
		{"must hold one hash", a + "\n" + b + "  other.tar.gz\n"},
	} {
		_, err := checksumFor([]byte(tc.body), archive, true)
		assert.ErrorContains(t, err, tc.want, "%q", tc.body)
	}
	_, err = checksumFor([]byte(a+"\n"), archive, false)
	assert.ErrorContains(t, err, "serve one archive", "a hash alone cannot serve several archives")
	dir, _ := url.Parse("https://example.com/v1/")
	_, err = checksumFor([]byte(a+"\n"), dir, true)
	assert.ErrorContains(t, err, "does not end with a file name")
}

// httpArchive returns a package archive of the given MANIFEST version whose
// library is signed with key, or not signed when key is nil.
func httpArchive(t *testing.T, version string, key *crypto.Key) []byte {
	t.Helper()
	lib := []byte("library " + version)
	files := []struct {
		name string
		body []byte
	}{
		{"MANIFEST", []byte("name = 'HTTP Driver'\nversion = '" + version + "'\n\n[Files]\ndriver = 'http.so'\n" +
			map[bool]string{true: "signature = 'http.so.sig'\n"}[key != nil])},
		{"http.so", lib},
	}
	if key != nil {
		signer, err := crypto.PGP().Sign().SigningKey(key).Detached().New()
		require.NoError(t, err)
		sig, err := signer.Sign(lib, crypto.Armor)
		require.NoError(t, err)
		files = append(files, struct {
			name string
			body []byte
		}{"http.so.sig", sig})
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(f.body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// httpFiles serves files of an HTTP source and counts the requests for each
// path.
type httpFiles struct {
	*httptest.Server
	mu       sync.Mutex
	files    map[string][]byte
	requests map[string]int
	// sent records whether any request carried a query or credentials.
	sent []string
}

func newHTTPFiles(t *testing.T) *httpFiles {
	f := &httpFiles{files: map[string][]byte{}, requests: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests[r.URL.Path]++
		if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
			f.sent = append(f.sent, r.URL.String())
		}
		body, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *httpFiles) count(p string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[p]
}

func (f *httpFiles) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.requests {
		n += c
	}
	return n
}

// httpBase is a base model that fetches the files of HTTP sources for real,
// with the registry functions of the test base model.
func httpBase() baseModel {
	base := testBaseModel()
	base.downloadHTTP = downloadHTTPPkg
	base.fetchHTTP = fetchHTTPFile
	base.projectSigningPolicy = projectSigningPolicy
	return base
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

const hostArchive = "/v1.0.0/drv_linux_amd64.tar.gz"

// httpList writes a dbc.toml with driver httpdrv from an HTTP source of f.
func (suite *SubcommandTestSuite) httpList(f *httpFiles, top, source string) string {
	return suite.writeList(top + "[drivers.httpdrv]\nversion = '=1.0.0'\n[drivers.httpdrv.source]\ntype = 'http'\n" +
		"url = '" + f.URL + "/v{version}/drv_{os}_{arch}.tar.gz'\n" + source)
}

func (suite *SubcommandTestSuite) skipUnlessLinuxAMD64() {
	if config.PlatformTuple() != "linux_amd64" {
		suite.T().Skip("the fixtures serve linux_amd64 archives")
	}
}

func (suite *SubcommandTestSuite) TestSyncHTTPSourceUnsigned() {
	suite.skipUnlessLinuxAMD64()
	f := newHTTPFiles(suite.T())
	archive := httpArchive(suite.T(), "1.0.0", nil)
	f.files[hostArchive] = archive
	listPath := suite.httpList(f, "", "signing_key = false\n")

	out := suite.runCmdWithRenderer(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(httpBase()), 0)
	suite.Contains(out, "comes from a plain http URL")
	suite.driverIsInstalledWithVersion("httpdrv", "1.0.0", true)
	d := suite.lockedDriverEntry("httpdrv")
	suite.Equal(lockSourceHTTP, d.Source.Type)
	suite.Empty(d.Source.URL)
	suite.Equal(lockedSigner{Type: lockSignerNone}, d.Signer)
	suite.Require().Len(d.Artifacts, 1)
	suite.Equal(f.URL+hostArchive, d.Artifacts[0].Location.Value)
	suite.Equal(lockHashPrefix+sha256Hex(archive), d.Artifacts[0].Hash)
	suite.Require().NotNil(d.Artifacts[0].Size)
	suite.EqualValues(len(archive), *d.Artifacts[0].Size)
	suite.Equal(1, f.count(hostArchive), "the measured download is installed")
	suite.Empty(f.sent, "no query or credentials are sent")

	// The lock entry is replayed: the installed driver is reused and no
	// new pin is made, so there is no warning either.
	lock := suite.readLockBytes()
	out = suite.runCmdWithRenderer(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(httpBase()), 0)
	suite.NotContains(out, "plain HTTP")
	suite.Equal(1, f.count(hostArchive))
	suite.Equal(lock, suite.readLockBytes())
}

func (suite *SubcommandTestSuite) TestLockHTTPSourceSigned() {
	suite.skipUnlessLinuxAMD64()
	key := generateHTTPTestKey(suite.T(), "acme")
	armored, err := key.GetArmoredPublicKey()
	suite.Require().NoError(err)
	keys := "[signing_keys.acme]\ntype = 'openpgp'\npublic_key = '''\n" + armored + "\n'''\n\n"

	suite.Run("the key is pinned", func() {
		f := newHTTPFiles(suite.T())
		f.files[hostArchive] = httpArchive(suite.T(), "1.0.0", key)
		listPath := suite.httpList(f, keys, "signing_key = 'acme'\n")
		suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
		d := suite.lockedDriverEntry("httpdrv")
		suite.Equal(lockedSigner{Type: lockSignerOpenPGP, Fingerprint: key.GetFingerprint()}, d.Signer)
		suite.Equal(lockHashPrefix+sha256Hex(f.files[hostArchive]), d.Artifacts[0].Hash)
		suite.Require().NoError(os.Remove(suite.lockPath()))
	})

	// dbc lock pins the archive without checking it; dbc sync then rejects
	// what it cannot install.
	for name, archive := range map[string][]byte{
		"another signer is pinned and fails to install":           httpArchive(suite.T(), "1.0.0", generateHTTPTestKey(suite.T(), "other")),
		"another MANIFEST version is pinned and fails to install": httpArchive(suite.T(), "1.0.1", key),
	} {
		suite.Run(name, func() {
			f := newHTTPFiles(suite.T())
			f.files[hostArchive] = archive
			listPath := suite.httpList(f, keys, "signing_key = 'acme'\n")
			suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
			suite.Equal(lockHashPrefix+sha256Hex(archive), suite.lockedDriverEntry("httpdrv").Artifacts[0].Hash)
			lock := suite.readLockBytes()
			out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(httpBase()))
			suite.Contains(out, map[bool]string{true: "signature", false: "1.0.1"}[strings.HasPrefix(name, "another signer")])
			suite.driverIsNotInstalled("httpdrv")
			suite.Equal(lock, suite.readLockBytes())
			suite.Require().NoError(os.Remove(suite.lockPath()))
		})
	}

	suite.Run("sync --no-verify skips only the signature", func() {
		f := newHTTPFiles(suite.T())
		f.files[hostArchive] = httpArchive(suite.T(), "1.0.0", generateHTTPTestKey(suite.T(), "other"))
		listPath := suite.httpList(f, keys, "signing_key = 'acme'\n")
		out := suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, NoVerify: true, Json: true}.GetModelCustom(httpBase()))
		suite.Contains(out, `"signature_verified":false`)
		suite.Equal(lockedSigner{Type: lockSignerOpenPGP, Fingerprint: key.GetFingerprint()}, suite.lockedDriverEntry("httpdrv").Signer)
		suite.driverIsInstalled("httpdrv", false)
		suite.Require().NoError(os.Remove(suite.lockPath()))
	})

	suite.Run("a key of the user config is not looked up", func() {
		f := newHTTPFiles(suite.T())
		listPath := suite.httpList(f, "", "signing_key = 'acme'\n")
		out := suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
		suite.Contains(out, `signing_key "acme" is not defined`)
		suite.Zero(f.total())
	})
}

func generateHTTPTestKey(t *testing.T, name string) *crypto.Key {
	t.Helper()
	key, err := crypto.PGP().KeyGeneration().AddUserId(name, name+"@example.invalid").New().GenerateKey()
	require.NoError(t, err)
	return key
}

func (suite *SubcommandTestSuite) TestLockHTTPSourceWithChecksums() {
	suite.skipUnlessLinuxAMD64()
	f := newHTTPFiles(suite.T())
	linux, macos := httpArchive(suite.T(), "1.0.0", nil), httpArchive(suite.T(), "1.0.0", nil)
	f.files[hostArchive] = linux
	f.files["/v1.0.0/SHA256SUMS"] = []byte(sha256Hex(linux) + "  drv_linux_amd64.tar.gz\n" + sha256Hex(macos) + "  drv_macos_arm64.tar.gz\n")
	listPath := suite.httpList(f, "platforms = ['linux_amd64', 'macos_arm64']\n\n",
		"signing_key = false\nchecksum_url = '"+f.URL+"/v{version}/SHA256SUMS'\n")

	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
	suite.Equal(1, f.total(), "only the checksum file is read")
	d := suite.lockedDriverEntry("httpdrv")
	suite.Require().Len(d.Artifacts, 2)
	for _, a := range d.Artifacts {
		suite.Nil(a.Size, "a published hash comes without a size")
	}
	suite.Equal(lockHashPrefix+sha256Hex(macos), d.Artifacts[1].Hash)

	// sync installs the host archive with the published hash.
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(httpBase()))
	suite.driverIsInstalledWithVersion("httpdrv", "1.0.0", true)
	suite.Equal(1, f.count(hostArchive))

	suite.Run("a checksum file that lacks an archive leaves the lock file as it is", func() {
		lock := suite.readLockBytes()
		suite.Require().NoError(os.Remove(suite.lockPath()))
		f.files["/v1.0.0/SHA256SUMS"] = []byte(sha256Hex(linux) + "  drv_linux_amd64.tar.gz\n")
		out := suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
		suite.Contains(out, "does not list drv_macos_arm64.tar.gz")
		suite.NoFileExists(suite.lockPath())
		suite.Require().NoError(os.WriteFile(suite.lockPath(), lock, 0o644))
	})
}

func (suite *SubcommandTestSuite) TestSyncHTTPSourceChecksTheLockFirst() {
	suite.skipUnlessLinuxAMD64()
	f := newHTTPFiles(suite.T())
	f.files[hostArchive] = httpArchive(suite.T(), "1.0.0", nil)
	listPath := suite.httpList(f, "", "signing_key = false\n")
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
	lock := suite.readLockBytes()
	requests := f.total()

	suite.Run("a recorded URL the template does not give", func() {
		tampered := bytes.Replace(lock, []byte("drv_linux_amd64"), []byte("evil_linux_amd64"), 1)
		suite.Require().NoError(os.WriteFile(suite.lockPath(), tampered, 0o644))
		out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(httpBase()))
		suite.Contains(out, "evil_linux_amd64")
		suite.Equal(requests, f.total())
		suite.Equal(tampered, suite.readLockBytes())
	})

	suite.Run("another URL template", func() {
		suite.Require().NoError(os.WriteFile(suite.lockPath(), lock, 0o644))
		other := suite.writeList("[drivers.httpdrv]\nversion = '=1.0.0'\n[drivers.httpdrv.source]\ntype = 'http'\n" +
			"url = '" + f.URL + "/other/v{version}/drv_{os}_{arch}.tar.gz'\nsigning_key = false\n")
		out := suite.runCmdErr(SyncCmd{Path: other, Level: suite.configLevel}.GetModelCustom(httpBase()))
		suite.Contains(out, "differs from the one it was locked with")
		suite.Equal(requests, f.total())
		suite.Equal(lock, suite.readLockBytes())
	})

	suite.Run("the HTTP source is gone from the driver list", func() {
		suite.Require().NoError(os.WriteFile(suite.lockPath(), lock, 0o644))
		other := suite.writeList("[drivers.httpdrv]\n")
		out := suite.runCmdErr(SyncCmd{Path: other, Level: suite.configLevel}.GetModelCustom(httpBase()))
		suite.Contains(out, "no longer declares")
		suite.Equal(lock, suite.readLockBytes())
	})

	suite.Run("an unsigned source cannot move to another", func() {
		suite.Require().NoError(os.WriteFile(suite.lockPath(), lock, 0o644))
		other := suite.writeList("[drivers.httpdrv]\nversion = '=1.0.0'\n[drivers.httpdrv.source]\ntype = 'http'\n" +
			"url = '" + f.URL + "/other/v{version}/drv_{os}_{arch}.tar.gz'\nsigning_key = false\n")
		out := suite.runCmdErr(LockCmd{Path: other}.GetModelCustom(httpBase()))
		suite.Contains(out, "another signer")
		suite.Equal(lock, suite.readLockBytes())
	})
}

func (suite *SubcommandTestSuite) TestHTTPSourceDeclaration() {
	f := newHTTPFiles(suite.T())
	for name, tc := range map[string]struct{ version, url, source, want string }{
		"unknown key":   {"=1.0.0", f.URL, "signing_key = false\ntargets = {}\n", `an HTTP source has no key "targets"`},
		"not exact":     {">=1.0.0", f.URL, "signing_key = false\n", "needs an exact version"},
		"prerelease":    {"=1.0.0", f.URL, "signing_key = false\n", "prerelease does not apply"},
		"http checksum": {"=1.0.0", "https://example.invalid", "checksum_url = 'http://example.invalid/SHA256SUMS'\n", "checksum_url must use https"},
	} {
		suite.Run(name, func() {
			prerelease := ""
			if name == "prerelease" {
				prerelease = "prerelease = 'allow'\n"
			}
			listPath := suite.writeList("[drivers.httpdrv]\nversion = '" + tc.version + "'\n" + prerelease + "[drivers.httpdrv.source]\ntype = 'http'\n" +
				"url = '" + tc.url + "/v{version}/drv_{os}_{arch}.tar.gz'\n" + tc.source)
			out := suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
			suite.Contains(out, tc.want)
			suite.NoFileExists(suite.lockPath())
		})
	}
	suite.Zero(f.total())
}

// A checksum file of a hash alone serves one archive only, also when two
// drivers name it.
func (suite *SubcommandTestSuite) TestLockHTTPSourcesShareAChecksumFile() {
	suite.skipUnlessLinuxAMD64()
	f := newHTTPFiles(suite.T())
	f.files["/SHA256SUM"] = []byte(strings.Repeat("a", 64) + "\n")
	driver := func(name string) string {
		return "[drivers." + name + "]\nversion = '=1.0.0'\n[drivers." + name + ".source]\ntype = 'http'\n" +
			"url = '" + f.URL + "/" + name + "_{os}_{arch}.tar.gz'\nchecksum_url = '" + f.URL + "/SHA256SUM'\nsigning_key = false\n"
	}
	listPath := suite.writeList(driver("one") + driver("two"))
	out := suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
	suite.Contains(out, "a checksum file with a hash alone must hold one hash and serve one archive")
	suite.NoFileExists(suite.lockPath())
}

func (suite *SubcommandTestSuite) TestHTTPSourceLockChanges() {
	suite.skipUnlessLinuxAMD64()
	f := newHTTPFiles(suite.T())
	linux := httpArchive(suite.T(), "1.0.0", nil)
	f.files[hostArchive] = linux
	listPath := suite.httpList(f, "", "signing_key = false\n")
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
	host := suite.lockedDriverEntry("httpdrv").Artifacts[0]

	suite.Run("a declared platform is added without measuring the others again", func() {
		macos := httpArchive(suite.T(), "1.0.0", nil)
		f.files["/v1.0.0/drv_macos_arm64.tar.gz"] = macos
		listPath := suite.httpList(f, "platforms = ['linux_amd64', 'macos_arm64']\n\n", "signing_key = false\n")
		before := f.count(hostArchive)
		suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
		suite.Equal(before, f.count(hostArchive), "the recorded archive is not downloaded again")
		suite.Equal(1, f.count("/v1.0.0/drv_macos_arm64.tar.gz"))
		d := suite.lockedDriverEntry("httpdrv")
		suite.Require().Len(d.Artifacts, 2)
		suite.Equal(host, d.Artifacts[0])
		suite.Equal(lockHashPrefix+sha256Hex(macos), d.Artifacts[1].Hash)
		suite.Require().NotNil(d.Artifacts[1].Size)
	})

	suite.Run("another version of the same source is locked anew", func() {
		other := httpArchive(suite.T(), "1.0.0+build.2", nil)
		f.files["/v1.0.0+build.2/drv_linux_amd64.tar.gz"] = other
		listPath := suite.writeList("[drivers.httpdrv]\nversion = '=1.0.0+build.2'\n[drivers.httpdrv.source]\ntype = 'http'\n" +
			"url = '" + f.URL + "/v{version}/drv_{os}_{arch}.tar.gz'\nsigning_key = false\n")
		suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(httpBase()))
		d := suite.lockedDriverEntry("httpdrv")
		suite.Equal("1.0.0+build.2", d.Version)
		suite.Require().Len(d.Artifacts, 1, "the artifacts of the old version are not kept")
		suite.Equal(lockHashPrefix+sha256Hex(other), d.Artifacts[0].Hash)
		suite.driverIsInstalledWithVersion("httpdrv", "1.0.0+build.2", true)
	})

	suite.Run("no fallback to the registries when the HTTP source is gone", func() {
		lock := suite.readLockBytes()
		listPath := suite.writeList("[drivers.httpdrv]\n")
		for _, cmd := range []commandModel{SyncCmd{Path: listPath, Level: suite.configLevel}, LockCmd{Path: listPath}} {
			var counters syncCounters
			out := suite.runCmdErr(cmd.GetModelCustom(counters.counted(httpBase())))
			suite.Contains(out, "which the driver list no longer declares")
			suite.Zero(counters.index)
			suite.Zero(counters.boundIndex)
			suite.Equal(lock, suite.readLockBytes())
		}
	})
}

// TestSourceSwitchBetweenHTTPAndRegistry pins that a driver moves between an
// HTTP source and an explicitly chosen registry only through dbc lock, and
// only when both sources have the same signer. A registry that serves the
// driver too is never used while the driver list names the HTTP source.
func (suite *SubcommandTestSuite) TestSourceSwitchBetweenHTTPAndRegistry() {
	suite.skipUnlessLinuxAMD64()
	key := generateHTTPTestKey(suite.T(), "acme")
	armored, err := key.GetArmoredPublicKey()
	suite.Require().NoError(err)
	registryKey, err := dbc.NewSigningKey([]byte(armored))
	suite.Require().NoError(err)
	otherKey := generateHTTPTestKey(suite.T(), "other")
	otherArmored, err := otherKey.GetArmoredPublicKey()
	suite.Require().NoError(err)
	keys := "[signing_keys.acme]\ntype = 'openpgp'\npublic_key = '''\n" + armored + "\n'''\n\n" +
		"[signing_keys.other]\ntype = 'openpgp'\npublic_key = '''\n" + otherArmored + "\n'''\n\n"
	signedWith := func(k *crypto.Key) lockedSigner {
		return lockedSigner{Type: lockSignerOpenPGP, Fingerprint: k.GetFingerprint()}
	}

	const driver = "test-driver-1"
	setup := func(policy dbc.SigningPolicy) (*httpFiles, *syncCounters, baseModel) {
		if err := os.Remove(suite.lockPath()); !errors.Is(err, os.ErrNotExist) {
			suite.Require().NoError(err)
		}
		f := newHTTPFiles(suite.T())
		regs := newTwoRegistries()
		regs.second.Signing = policy
		var counters syncCounters
		base := counters.counted(regs.base())
		base.downloadHTTP = downloadHTTPPkg
		base.fetchHTTP = fetchHTTPFile
		base.projectSigningPolicy = projectSigningPolicy
		return f, &counters, base
	}
	httpSource := func(f *httpFiles, signingKey string) string {
		return suite.writeList(keys + "[drivers." + driver + "]\nversion = '=1.0.0'\n[drivers." + driver + ".source]\ntype = 'http'\n" +
			"url = '" + f.URL + "/v{version}/drv_{os}_{arch}.tar.gz'\nsigning_key = " + signingKey + "\n")
	}
	registrySource := func(version string) string {
		return suite.writeList(keys + "[drivers." + driver + "]\nversion = '" + version + "'\n" +
			"source = { type = 'registry', url = '" + secondRegistryURL + "' }\n")
	}

	suite.Run("from HTTP to a registry with the same key", func() {
		f, counters, base := setup(dbc.SignWith(registryKey))
		f.files[hostArchive] = httpArchive(suite.T(), "1.0.0", key)
		listPath := httpSource(f, "'acme'")
		suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(base))
		old := suite.lockedDriverEntry(driver)
		suite.Equal(lockSourceHTTP, old.Source.Type)
		suite.Zero(counters.index+counters.boundIndex, "the registries are not searched for a driver of an HTTP source")
		lock := suite.readLockBytes()
		requests := f.total()

		registrySource(">=1.0, <2.0")
		out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(base))
		suite.Contains(out, "the lock file has it from an HTTP source; run `dbc lock` to resolve it again from "+secondRegistryURL)
		suite.Zero(counters.index + counters.boundIndex + counters.downloads)
		suite.Equal(lock, suite.readLockBytes())
		suite.driverIsNotInstalled(driver)

		suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(base))
		d := suite.lockedDriverEntry(driver)
		suite.Equal(lockedSource{Type: lockSourceRegistry, URL: secondRegistryURL}, d.Source)
		suite.Equal("1.1.0", d.Version, "the version is resolved again from the registry")
		suite.Equal(signedWith(key), d.Signer)
		suite.Require().Len(d.Artifacts, 1)
		suite.Equal(resolution.ArtifactLocationRegistryPath, d.Artifacts[0].Location.Kind)
		suite.NotEqual(old.Artifacts[0].Hash, d.Artifacts[0].Hash)
		suite.Zero(counters.index, "only the chosen registry is read")
		suite.Equal(requests, f.total(), "the HTTP source is not read again")
	})

	suite.Run("from a registry to HTTP with the same key", func() {
		f, counters, base := setup(dbc.SignWith(registryKey))
		archive := httpArchive(suite.T(), "1.0.0", key)
		f.files[hostArchive] = archive
		listPath := registrySource("=1.0.0")
		suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(base))
		old := suite.lockedDriverEntry(driver)
		suite.Equal(secondRegistryURL, old.Source.URL)
		lock := suite.readLockBytes()

		httpSource(f, "'acme'")
		reads := counters.index + counters.boundIndex
		out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(base))
		suite.Contains(out, "the lock file has it from registry "+secondRegistryURL+"; run `dbc lock` to lock it again")
		suite.Zero(f.total())
		suite.Equal(lock, suite.readLockBytes())
		suite.driverIsNotInstalled(driver)

		suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(base))
		d := suite.lockedDriverEntry(driver)
		suite.Equal(lockSourceHTTP, d.Source.Type)
		suite.NotEmpty(d.Source.TemplateSHA256)
		suite.Equal(old.Version, d.Version)
		suite.Equal(signedWith(key), d.Signer)
		suite.Require().Len(d.Artifacts, 1)
		suite.Equal(f.URL+hostArchive, d.Artifacts[0].Location.Value, "the same version now comes from the HTTP source")
		suite.Equal(lockHashPrefix+sha256Hex(archive), d.Artifacts[0].Hash)
		suite.Equal(reads, counters.index+counters.boundIndex, "the registries are not read")

		out = suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(base))
		suite.Contains(out, `"signature_verified":true`)
		suite.driverIsInstalledWithVersion(driver, "1.0.0", true)
	})

	suite.Run("from an unsigned HTTP source to an unsigned registry", func() {
		f, counters, base := setup(dbc.Unsigned())
		f.files[hostArchive] = httpArchive(suite.T(), "1.0.0", nil)
		listPath := httpSource(f, "false")
		suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(base))
		lock := suite.readLockBytes()

		registrySource(">=1.0, <2.0")
		out := suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(base))
		suite.Contains(out, "the lock file has it from the unsigned HTTP source, but it would now come from the unsigned registry "+secondRegistryURL+", which is another signer")
		suite.Zero(counters.downloads)
		suite.Equal(lock, suite.readLockBytes())
	})

	suite.Run("from a signed registry to HTTP with another key", func() {
		f, _, base := setup(dbc.SignWith(registryKey))
		f.files[hostArchive] = httpArchive(suite.T(), "1.0.0", otherKey)
		listPath := registrySource("=1.0.0")
		suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(base))
		lock := suite.readLockBytes()

		httpSource(f, "'other'")
		out := suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(base))
		suite.Contains(out, "the lock file pins the signing key "+key.GetFingerprint()+", but its packages would now be verified with the signing key "+otherKey.GetFingerprint())
		suite.Zero(f.total())
		suite.Equal(lock, suite.readLockBytes())
	})
}

// TestSyncHTTPSourceVerifiesWhenInstalling pins that dbc sync pins a measured
// archive before installing it, like dbc lock, so an archive whose signature
// does not verify is pinned but not installed.
func (suite *SubcommandTestSuite) TestSyncHTTPSourceVerifiesWhenInstalling() {
	suite.skipUnlessLinuxAMD64()
	key := generateHTTPTestKey(suite.T(), "acme")
	armored, err := key.GetArmoredPublicKey()
	suite.Require().NoError(err)
	f := newHTTPFiles(suite.T())
	f.files[hostArchive] = httpArchive(suite.T(), "1.0.0", generateHTTPTestKey(suite.T(), "other"))
	listPath := suite.httpList(f, "[signing_keys.acme]\ntype = 'openpgp'\npublic_key = '''\n"+armored+"\n'''\n\n", "signing_key = 'acme'\n")
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(httpBase()))
	suite.Contains(out, "failed to verify signature")
	suite.Equal(lockHashPrefix+sha256Hex(f.files[hostArchive]), suite.lockedDriverEntry("httpdrv").Artifacts[0].Hash)
	suite.driverIsNotInstalled("httpdrv")
}

// dbc add writes back a driver list with an HTTP source as it was.
func (suite *SubcommandTestSuite) TestAddKeepsAnHTTPSource() {
	for _, version := range []string{"=1.0.0+build.1", "1.0.0+build.1"} {
		suite.Run(version, func() {
			http := "[drivers.httpdrv]\nversion = '" + version + "'\n[drivers.httpdrv.source]\ntype = 'http'\n" +
				"url = 'https://example.com/v{version}/drv_{os}_{arch}.tar.gz'\nchecksum_url = 'https://example.com/v{version}/SHA256SUMS'\nsigning_key = false\n"
			listPath := suite.writeList(http)
			suite.runCmd(AddCmd{Path: listPath, Driver: []string{"test-driver-1"}}.GetModelCustom(testBaseModel()))
			data, err := os.ReadFile(listPath)
			suite.Require().NoError(err)
			suite.Contains(string(data), "version = '"+version+"'")
			list, err := openAndDecodeDriverList(listPath)
			suite.Require().NoError(err)
			spec := list.Drivers["httpdrv"]
			suite.Require().True(spec.isHTTP())
			suite.Equal("1.0.0+build.1", spec.exact.String())
			suite.Equal("https://example.com/v{version}/drv_{os}_{arch}.tar.gz", *spec.Source.URL)
			suite.Equal("https://example.com/v{version}/SHA256SUMS", *spec.Source.ChecksumURL)
			suite.Equal(false, spec.Source.SigningKey)

			out := suite.runCmdErr(AddCmd{Path: listPath, Driver: []string{"httpdrv"}}.GetModelCustom(testBaseModel()))
			suite.Contains(out, "dbc add does not change")
		})
	}
}

// A file name of a checksum file names one archive URL only.
func (suite *SubcommandTestSuite) TestLockHTTPSourcesWithTheSameFileName() {
	suite.skipUnlessLinuxAMD64()
	f := newHTTPFiles(suite.T())
	f.files["/SHA256SUMS"] = []byte(strings.Repeat("a", 64) + "  drv.tar.gz\n")
	driver := func(name string) string {
		return "[drivers." + name + "]\nversion = '=1.0.0'\n[drivers." + name + ".source]\ntype = 'http'\n" +
			"url = '" + f.URL + "/" + name + "/drv.tar.gz'\nchecksum_url = '" + f.URL + "/SHA256SUMS'\nsigning_key = false\n"
	}
	listPath := suite.writeList(driver("one") + driver("two"))
	out := suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(httpBase()))
	suite.Contains(out, "have the same file name drv.tar.gz")
	suite.Zero(f.total())
	suite.NoFileExists(suite.lockPath())
}
