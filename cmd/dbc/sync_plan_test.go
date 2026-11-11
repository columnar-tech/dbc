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
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/go-faster/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const planTarget = "linux_amd64"

var (
	planHashA = strings.Repeat("a", 64)
	planHashB = strings.Repeat("b", 64)
	planHashC = strings.Repeat("c", 64)
	planHashD = strings.Repeat("d", 64)
	planHashE = strings.Repeat("e", 64)
)

const planIndexYAML = `drivers:
  - name: Alpha
    path: alpha
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: alpha/1.0.0/alpha-linux.tar.gz
            hash: sha256:` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `
          - platform: macos_arm64
            url: alpha/1.0.0/alpha-macos.tar.gz
            hash: sha256:` + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + `
      - version: v1.1.0
        packages:
          - platform: linux_amd64
            url: alpha/1.1.0/alpha-linux.tar.gz
            hash: sha256:` + "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" + `
          - platform: macos_arm64
            url: alpha/1.1.0/alpha-macos.tar.gz
            hash: sha256:` + "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" + `
          - platform: windows_amd64
            url: alpha/1.1.0/alpha-windows.tar.gz
      - version: v2.0.0-beta.1
        packages:
          - platform: linux_amd64
            url: alpha/2.0.0-beta.1/alpha-linux.tar.gz
            hash: sha256:` + "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" + `
  - name: Beta
    path: beta
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: beta/1.0.0/beta-linux.tar.gz
          - platform: macos_arm64
            url: beta/1.0.0/beta-macos.tar.gz
  - name: Bad
    path: bad
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: bad/1.0.0/bad-linux.tar.gz
            hash: sha256:` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `
          - platform: macos_arm64
            url: bad/1.0.0/bad-macos.tar.gz
            hash: sha256:nothex
  - name: BadCurrent
    path: bad-current
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: bad-current/1.0.0/bad-linux.tar.gz
            hash: sha256:nothex
`

var planRegistryURL = must(url.Parse("https://registry.example.com"))

// planFixture records which registry accesses a planner made.
type planFixture struct {
	t            *testing.T
	registries   []dbc.Registry
	indexCalls   int
	boundIndexed []*url.URL
	planner      syncPlanner
}

func newPlanFixture(t *testing.T) *planFixture {
	f := &planFixture{t: t, registries: []dbc.Registry{{BaseURL: planRegistryURL}}}
	decode := func() ([]dbc.Driver, error) {
		var decoded struct {
			Drivers []dbc.Driver `yaml:"drivers"`
		}
		if err := yaml.Unmarshal([]byte(planIndexYAML), &decoded); err != nil {
			return nil, err
		}
		for i := range decoded.Drivers {
			decoded.Drivers[i].Registry = &dbc.Registry{BaseURL: planRegistryURL}
		}
		return decoded.Drivers, nil
	}
	f.planner = syncPlanner{
		target:     planTarget,
		registries: func() ([]dbc.Registry, error) { return f.registries, nil },
		index: func() ([]dbc.Driver, error) {
			f.indexCalls++
			return decode()
		},
		registryIndex: func(r dbc.Registry) ([]dbc.Driver, error) {
			f.boundIndexed = append(f.boundIndexed, r.BaseURL)
			return decode()
		},
	}
	return f
}

func constraint(t *testing.T, s string) *semver.Constraints {
	c, err := semver.NewConstraint(s)
	require.NoError(t, err)
	return c
}

func lockedAlpha(version string, artifacts ...lockedArtifact) lockedDriver {
	return lockedDriver{
		Name:      "alpha",
		Version:   version,
		Signer:    testSigner,
		Source:    lockedSource{Type: lockSourceRegistry, URL: "https://registry.example.com"},
		Artifacts: artifacts,
	}
}

func testSyncTarget(t *testing.T, tuple string) resolution.Target {
	t.Helper()
	target, err := resolution.TargetFromPlatformTuple(tuple)
	require.NoError(t, err)
	return target
}

func pathArtifact(t *testing.T, tuple, path, hash string) lockedArtifact {
	return lockedArtifact{
		Target:   testSyncTarget(t, tuple),
		Format:   "tar.gz",
		Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationRegistryPath, Value: path},
		Hash:     lockHashPrefix + hash,
	}
}

func v2Lock(drivers ...lockedDriver) parsedLockFile {
	return parsedLockFile{Version: lockFileVersionV2, V2: lockFileV2{Version: lockFileVersionV2, Drivers: drivers}}
}

func v1Lock(infos ...lockInfo) parsedLockFile {
	lf := LockFile{Version: lockFileVersion, Drivers: infos, lockinfo: map[string]lockInfo{}}
	for _, info := range infos {
		lf.lockinfo[info.Name] = info
	}
	return parsedLockFile{Version: lockFileVersion, V1: lf}
}

func listOf(drivers map[string]driverSpec) DriversList { return DriversList{Drivers: drivers} }

func artifactTargets(d lockedDriver) map[resolution.Target]string {
	out := map[resolution.Target]string{}
	for _, a := range d.Artifacts {
		out[a.Target] = a.Hash
	}
	return out
}

func TestPlanResolvesAllPublishedTargets(t *testing.T) {
	f := newPlanFixture(t)
	planned, warnings, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), parsedLockFile{})
	require.NoError(t, err)
	assert.Empty(t, warnings)
	require.Len(t, planned, 1)

	pd := planned[0]
	assert.Equal(t, "1.1.0", pd.Entry.Version)
	assert.Equal(t, lockedSource{Type: lockSourceRegistry, URL: "https://registry.example.com"}, pd.Entry.Source)
	// The windows target publishes no hash and is not the current target.
	assert.Equal(t, map[resolution.Target]string{
		testSyncTarget(t, "linux_amd64"): lockHashPrefix + planHashC,
		testSyncTarget(t, "macos_arm64"): lockHashPrefix + planHashD,
	}, artifactTargets(pd.Entry))
	assert.Equal(t, planHashC, pd.Package.ArchiveSHA256)
	assert.Equal(t, "https://registry.example.com/alpha/1.1.0/alpha-linux.tar.gz", pd.Package.Path.String())
	for _, a := range pd.Entry.Artifacts {
		assert.Equal(t, "tar.gz", a.Format)
		assert.Equal(t, resolution.ArtifactLocationRegistryPath, a.Location.Kind)
	}
	assert.Equal(t, 1, f.indexCalls)
	require.NoError(t, v2Lock(pd.Entry).V2.validate())
}

func TestPlanPartialLockKeepsCurrentTargetWithoutHash(t *testing.T) {
	f := newPlanFixture(t)
	planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"beta": {}}), parsedLockFile{})
	require.NoError(t, err)
	require.Len(t, planned, 1)

	// Only the current target is recorded; its hash is measured later.
	assert.Equal(t, map[resolution.Target]string{testSyncTarget(t, "linux_amd64"): ""}, artifactTargets(planned[0].Entry))
	assert.Empty(t, planned[0].Package.ArchiveSHA256)
}

func TestPlanSkipsMalformedSecondaryHash(t *testing.T) {
	f := newPlanFixture(t)
	planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"bad": {}}), parsedLockFile{})
	require.NoError(t, err)
	require.Len(t, planned, 1)
	assert.Equal(t, map[resolution.Target]string{
		testSyncTarget(t, "linux_amd64"): lockHashPrefix + planHashA,
	}, artifactTargets(planned[0].Entry))
}

func TestPlanRejectsMalformedCurrentHash(t *testing.T) {
	f := newPlanFixture(t)
	_, _, err := f.planner.plan(listOf(map[string]driverSpec{"bad-current": {}}), parsedLockFile{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid archive hash")
}

func TestPlanUnknownDriverReportsRegistryErrors(t *testing.T) {
	f := newPlanFixture(t)
	inner := f.planner.index
	f.planner.index = func() ([]dbc.Driver, error) {
		drivers, _ := inner()
		return drivers, errors.New("registry https://backup.example.com: timeout")
	}
	_, _, err := f.planner.plan(listOf(map[string]driverSpec{"missing": {}}), parsedLockFile{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "driver `missing` not found")
	assert.Contains(t, err.Error(), "Some driver registries were unavailable")
}

func TestPlanReplaysLockWithoutRegistryIndex(t *testing.T) {
	f := newPlanFixture(t)
	lock := v2Lock(lockedAlpha("1.0.0", pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA)))
	planned, warnings, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), lock)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	require.Len(t, planned, 1)

	pd := planned[0]
	assert.Equal(t, "1.0.0", pd.Package.Version.String(), "the locked version wins over the newer 1.1.0")
	assert.Equal(t, planHashA, pd.Package.ArchiveSHA256)
	assert.Equal(t, "https://registry.example.com/alpha/1.0.0/alpha-linux.tar.gz", pd.Package.Path.String())
	assert.Equal(t, lock.V2.Drivers[0], pd.Entry)
	assert.Zero(t, f.indexCalls)
	assert.Empty(t, f.boundIndexed)
}

func TestPlanReplayUsesAbsoluteArtifactURL(t *testing.T) {
	f := newPlanFixture(t)
	abs := "https://mirror.example.net/alpha-linux.tar.gz"
	d := lockedAlpha("1.0.0", lockedArtifact{
		Target:   testSyncTarget(t, planTarget),
		Format:   "tar.gz",
		Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: abs},
		Hash:     lockHashPrefix + planHashA,
	})
	planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), v2Lock(d))
	require.NoError(t, err)
	assert.Equal(t, abs, planned[0].Package.Path.String())
}

func TestPlanReplayBindsRegistry(t *testing.T) {
	lock := v2Lock(lockedAlpha("1.0.0", pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA)))
	for name, registries := range map[string][]dbc.Registry{
		"none":      {{BaseURL: must(url.Parse("https://other.example.com"))}},
		"ambiguous": {{BaseURL: planRegistryURL}, {BaseURL: must(url.Parse("https://REGISTRY.example.com/"))}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newPlanFixture(t)
			f.registries = registries
			_, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), lock)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "https://registry.example.com")
		})
	}
}

func TestPlanReplayRebindsCredentialsOfConfiguredRegistry(t *testing.T) {
	f := newPlanFixture(t)
	f.registries = []dbc.Registry{{BaseURL: must(url.Parse("https://user:SUPERSECRET@registry.example.com?token=SUPERSECRET"))}}
	lock := v2Lock(lockedAlpha("1.0.0", pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA)))
	planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), lock)
	require.NoError(t, err)
	assert.Equal(t, "https://user:SUPERSECRET@registry.example.com/alpha/1.0.0/alpha-linux.tar.gz?token=SUPERSECRET", planned[0].Package.Path.String())
	assert.NotContains(t, planned[0].Entry.Source.URL, "SUPERSECRET")
}

func TestPlanCompletesMissingTarget(t *testing.T) {
	f := newPlanFixture(t)
	existing := pathArtifact(t, "macos_arm64", "alpha/1.0.0/alpha-macos.tar.gz", planHashB)
	lock := v2Lock(lockedAlpha("1.0.0", existing))
	planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), lock)
	require.NoError(t, err)
	require.Len(t, planned, 1)

	assert.Equal(t, map[resolution.Target]string{
		testSyncTarget(t, "macos_arm64"): lockHashPrefix + planHashB,
		testSyncTarget(t, "linux_amd64"): lockHashPrefix + planHashA,
	}, artifactTargets(planned[0].Entry))
	assert.Equal(t, existing, planned[0].Entry.Artifacts[0], "existing artifacts stay unchanged")
	assert.Equal(t, planHashA, planned[0].Package.ArchiveSHA256)
	assert.Equal(t, "1.0.0", planned[0].Package.Version.String())
	assert.Zero(t, f.indexCalls, "only the bound registry's index is fetched")
	require.Len(t, f.boundIndexed, 1)
	assert.Equal(t, planRegistryURL.String(), f.boundIndexed[0].String())
	assert.Len(t, lock.V2.Drivers[0].Artifacts, 1, "the lock that was read is not modified")
}

func TestPlanCompletionWithoutPublishedHashMeasuresLater(t *testing.T) {
	f := newPlanFixture(t)
	d := lockedAlpha("1.1.0", pathArtifact(t, "macos_arm64", "alpha/1.1.0/alpha-macos.tar.gz", planHashD))
	d.Name = "beta"
	d.Version = "1.0.0"
	d.Artifacts = []lockedArtifact{pathArtifact(t, "macos_arm64", "beta/1.0.0/beta-macos.tar.gz", planHashD)}
	planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"beta": {}}), v2Lock(d))
	require.NoError(t, err)
	assert.Equal(t, "", artifactTargets(planned[0].Entry)[testSyncTarget(t, "linux_amd64")])
	assert.Empty(t, planned[0].Package.ArchiveSHA256)
}

func TestPlanCompletionErrors(t *testing.T) {
	macos := pathArtifact(t, "macos_arm64", "alpha/1.0.0/alpha-macos.tar.gz", planHashB)

	t.Run("registry with query", func(t *testing.T) {
		f := newPlanFixture(t)
		f.registries = []dbc.Registry{{BaseURL: must(url.Parse("https://registry.example.com?tenant=SUPERSECRET"))}}
		_, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), v2Lock(lockedAlpha("1.0.0", macos)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lock it again")
		assert.NotContains(t, err.Error(), "SUPERSECRET")
		assert.Empty(t, f.boundIndexed)
	})
	t.Run("version missing from the index", func(t *testing.T) {
		f := newPlanFixture(t)
		_, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), v2Lock(lockedAlpha("0.9.0", macos)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "0.9.0")
	})
	t.Run("malformed hash of the current target", func(t *testing.T) {
		f := newPlanFixture(t)
		d := lockedAlpha("1.0.0", macos)
		d.Name = "bad-current"
		_, _, err := f.planner.plan(listOf(map[string]driverSpec{"bad-current": {}}), v2Lock(d))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid archive hash")
	})
}

func TestPlanNewEntryNeedsUniqueRegistry(t *testing.T) {
	f := newPlanFixture(t)
	f.registries = []dbc.Registry{
		{BaseURL: planRegistryURL},
		{BaseURL: must(url.Parse("https://user:SUPERSECRET@registry.example.com?tenant=SUPERSECRET"))},
	}
	_, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), parsedLockFile{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
	assert.NotContains(t, err.Error(), "SUPERSECRET")
}

func TestRedactURLSecrets(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"userinfo", `Get http://user:SECRET@host:8080/index.yaml: refused`, `Get http://host:8080/index.yaml: refused`},
		{"encoded userinfo", `registry https://us%40er:p%2Fw%3F@host/base: bad`, `registry https://host/base: bad`},
		{"percent-encoded query", `fetch https://host/a?token=abc%2Fdef&x=y%20z: 500`, `fetch https://host/a: 500`},
		{"multiple query values", `https://host/p?a=1&a=2&b=SECRET#frag failed`, `https://host/p failed`},
		{"query on bare host", `registry http://u:p@host?token=SECRET: x`, `registry http://host: x`},
		{"quoted URL with spaces", `Get "https://u:p@host/p?token=a b c": EOF`, `Get "https://host/p": EOF`},
		{"single quotes", `url 'http://u:p@host/p?t=a b' bad`, `url 'http://host/p' bad`},
		{"parenthesis", `(https://host/p?t=SECRET) closed`, `(https://host/p) closed`},
		{"several URLs", `http://a:b@one/x?k=v and HTTPS://c:d@two/y?k=w`, `http://one/x and HTTPS://two/y`},
		{"single quotes inside the query", `fetch https://host/a?token='SUPERSECRET': failed`, `fetch https://host/a': failed`},
		{"quoted URL with the other quote inside", `Get "https://host/a?token='SUPERSECRET'": dial`, `Get "https://host/a": dial`},
		{"brackets inside the query", `fetch https://host/a?token=(SUPERSECRET)x&y=<z> failed`, `fetch https://host/a failed`},
		{"escaped quotes inside the query", `Get "https://host/a?token=\"SUPERSECRET\"": EOF`, `Get "https://host/a": EOF`},
		{"no URL", `plain error: connection refused`, `plain error: connection refused`},
	}
	base := errors.New("base")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := redactURLSecrets(fmt.Errorf("%s: %w", tt.in, base), nil)
			assert.Equal(t, tt.want+": base", err.Error())
			assert.ErrorIs(t, err, base)
		})
	}
	assert.NoError(t, redactURLSecrets(nil, nil))
}

func TestRedactKnownRegistrySecrets(t *testing.T) {
	base := errors.New("base")
	registries := []dbc.Registry{
		{BaseURL: must(url.Parse("https://us%40er-name:p%2Fw%20SECRETPASS@host/base?token=abc%2FSECRETTOKEN&a=q7&other='SECRETQUOTE'"))},
		{BaseURL: nil},
	}
	secrets := registrySecrets(registries)
	assert.Contains(t, secrets, "q7", "short values are collected too")

	for _, text := range []string{
		"proxy rejected user us@er-name",
		"password p/w SECRETPASS was refused",
		"escaped p%2Fw+SECRETPASS and p%2Fw%20SECRETPASS",
		"token abc/SECRETTOKEN abc%2FSECRETTOKEN",
		"value 'SECRETQUOTE' leaked",
	} {
		err := redactURLSecrets(errors.New(text), secrets)
		assert.NotContains(t, err.Error(), "SECRET", text)
		assert.Contains(t, err.Error(), "REDACTED", text)
	}
	// A short secret cannot be replaced safely in place, so the whole
	// diagnostic is hidden while errors.Is still works.
	short := redactURLSecrets(fmt.Errorf("token q7 rejected: %w", base), secrets)
	assert.NotContains(t, short.Error(), "q7")
	assert.ErrorIs(t, short, base)
	assert.Equal(t, "no secret here", redactURLSecrets(errors.New("no secret here"), secrets).Error())
}

func TestPlanConstraintAndPrerelease(t *testing.T) {
	lock10 := v2Lock(lockedAlpha("1.0.0", pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA)))
	lockBeta := v2Lock(lockedAlpha("2.0.0-beta.1", pathArtifact(t, "linux_amd64", "alpha/2.0.0-beta.1/alpha-linux.tar.gz", planHashE)))

	tests := []struct {
		name        string
		spec        func(*testing.T) driverSpec
		lock        parsedLockFile
		wantVersion string
		wantIndex   int
	}{
		{"locked version satisfies constraint", func(t *testing.T) driverSpec { return driverSpec{Version: constraint(t, "^1.0")} }, lock10, "1.0.0", 0},
		{"constraint no longer satisfied", func(t *testing.T) driverSpec { return driverSpec{Version: constraint(t, ">=1.1")} }, lock10, "1.1.0", 1},
		{"no usable entry resolves the newest", func(t *testing.T) driverSpec { return driverSpec{} }, parsedLockFile{}, "1.1.0", 1},
		{"prerelease allowed picks the prerelease", func(t *testing.T) driverSpec {
			return driverSpec{Version: constraint(t, ">=1.0.0"), Prerelease: "allow"}
		}, parsedLockFile{}, "2.0.0-beta.1", 1},
		{"prerelease not allowed skips it", func(t *testing.T) driverSpec { return driverSpec{Version: constraint(t, ">=1.0.0")} }, parsedLockFile{}, "1.1.0", 1},
		{"locked prerelease stays when allowed", func(t *testing.T) driverSpec {
			return driverSpec{Version: constraint(t, ">=1.0.0"), Prerelease: "allow"}
		}, lockBeta, "2.0.0-beta.1", 0},
		{"locked prerelease dropped when not allowed", func(t *testing.T) driverSpec { return driverSpec{Version: constraint(t, ">=1.0.0")} }, lockBeta, "1.1.0", 1},
		{"locked prerelease without constraint needs allow", func(t *testing.T) driverSpec { return driverSpec{} }, lockBeta, "1.1.0", 1},
		{"locked prerelease without constraint when allowed", func(t *testing.T) driverSpec { return driverSpec{Prerelease: "allow"} }, lockBeta, "2.0.0-beta.1", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPlanFixture(t)
			planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": tt.spec(t)}), tt.lock)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVersion, planned[0].Package.Version.String())
			assert.Equal(t, tt.wantIndex, f.indexCalls)
		})
	}
}

func TestPlanV1Lock(t *testing.T) {
	const checksum = "1111111111111111111111111111111111111111111111111111111111111111"
	info := func(version, platform string) parsedLockFile {
		return v1Lock(lockInfo{Name: "alpha", Version: semver.MustParse(version), Platform: platform, Checksum: checksum})
	}
	tests := []struct {
		name        string
		spec        func(*testing.T) driverSpec
		lock        parsedLockFile
		wantVersion string
		wantProof   string
		wantWarning []string
	}{
		{"same platform keeps pin and proof", func(*testing.T) driverSpec { return driverSpec{} }, info("1.0.0", planTarget), "1.0.0", checksum, nil},
		{"other platform drops proof", func(*testing.T) driverSpec { return driverSpec{} }, info("1.0.0", "macos_arm64"), "1.0.0", "",
			[]string{"alpha", "macos_arm64", "not recorded for this platform"}},
		{"empty platform drops proof", func(*testing.T) driverSpec { return driverSpec{} }, info("1.0.0", ""), "1.0.0", "",
			[]string{"alpha", "unknown", "not recorded for this platform"}},
		{"dropped pin drops proof", func(t *testing.T) driverSpec { return driverSpec{Version: constraint(t, ">=1.1")} }, info("1.0.0", planTarget), "1.1.0", "",
			[]string{"alpha", planTarget, "no longer satisfies"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPlanFixture(t)
			planned, warnings, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": tt.spec(t)}), tt.lock)
			require.NoError(t, err)
			require.Len(t, planned, 1)
			assert.Equal(t, tt.wantVersion, planned[0].Package.Version.String())
			assert.Equal(t, tt.wantProof, planned[0].LibrarySHA256)
			assert.Equal(t, 1, f.indexCalls, "v1 entries are resolved from the index")
			if tt.wantWarning == nil {
				assert.Empty(t, warnings)
				return
			}
			require.Len(t, warnings, 1)
			for _, want := range tt.wantWarning {
				assert.Contains(t, warnings[0], want)
			}
		})
	}

	t.Run("an entry without checksum warns about nothing", func(t *testing.T) {
		f := newPlanFixture(t)
		lock := v1Lock(lockInfo{Name: "alpha", Version: semver.MustParse("1.0.0")})
		planned, warnings, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), lock)
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.Empty(t, planned[0].LibrarySHA256)
		assert.Equal(t, "1.0.0", planned[0].Package.Version.String())
	})
}

func TestPlanOrderIsDeterministic(t *testing.T) {
	f := newPlanFixture(t)
	specs := map[string]driverSpec{"beta": {}, "alpha": {}}
	for range 5 {
		planned, _, err := f.planner.plan(listOf(specs), parsedLockFile{})
		require.NoError(t, err)
		require.Len(t, planned, 2)
		assert.Equal(t, "alpha", planned[0].Name)
		assert.Equal(t, "beta", planned[1].Name)
	}
}

func TestPlanFailsWhenNoRegistryDelivers(t *testing.T) {
	f := newPlanFixture(t)
	f.planner.index = func() ([]dbc.Driver, error) { return nil, errors.New("connection refused") }
	_, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), parsedLockFile{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error getting driver list")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestPlanRecordsAndPinsTheSigner(t *testing.T) {
	columnar, err := dbc.ColumnarSigningKey()
	require.NoError(t, err)

	t.Run("fresh resolution records the key of the source", func(t *testing.T) {
		f := newPlanFixture(t)
		planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), parsedLockFile{})
		require.NoError(t, err)
		assert.Same(t, columnar, planned[0].Signer)
		assert.Equal(t, lockedSigner{Type: lockSignerOpenPGP, Fingerprint: columnar.Fingerprint()}, planned[0].Entry.Signer)
	})

	t.Run("v1 lock upgrade records the key of the source", func(t *testing.T) {
		f := newPlanFixture(t)
		lock := v1Lock(lockInfo{Name: "alpha", Version: semver.MustParse("1.0.0"), Platform: "linux_amd64"})
		planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), lock)
		require.NoError(t, err)
		assert.Equal(t, columnar.Fingerprint(), planned[0].Entry.Signer.Fingerprint)
	})

	t.Run("replay uses the pinned key", func(t *testing.T) {
		f := newPlanFixture(t)
		lock := v2Lock(lockedAlpha("1.0.0", pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA)))
		planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), lock)
		require.NoError(t, err)
		assert.Same(t, columnar, planned[0].Signer)
		assert.Zero(t, f.indexCalls)
	})

	t.Run("completing a missing target keeps the signer", func(t *testing.T) {
		f := newPlanFixture(t)
		lock := v2Lock(lockedAlpha("1.0.0", pathArtifact(t, "macos_arm64", "alpha/1.0.0/alpha-macos.tar.gz", planHashB)))
		planned, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), lock)
		require.NoError(t, err)
		assert.Equal(t, testSigner, planned[0].Entry.Signer)
		assert.Same(t, columnar, planned[0].Signer)
	})

	t.Run("an untrusted pin fails before any registry access", func(t *testing.T) {
		f := newPlanFixture(t)
		d := lockedAlpha("1.0.0", pathArtifact(t, "macos_arm64", "alpha/1.0.0/alpha-macos.tar.gz", planHashB))
		d.Signer.Fingerprint = otherFingerprint
		_, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), v2Lock(d))
		require.Error(t, err)
		assert.Contains(t, err.Error(), otherFingerprint)
		assert.Contains(t, err.Error(), "delete the driver's entry")
		assert.Zero(t, f.indexCalls)
		assert.Empty(t, f.boundIndexed)
	})

	t.Run("resolving again may not replace the pin", func(t *testing.T) {
		f := newPlanFixture(t)
		d := lockedAlpha("1.0.0", pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA))
		d.Signer.Fingerprint = otherFingerprint
		spec := driverSpec{Version: constraint(t, ">=1.1")}
		_, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": spec}), v2Lock(d))
		require.Error(t, err)
		assert.Contains(t, err.Error(), otherFingerprint)
		assert.Contains(t, err.Error(), "delete the driver's entry")
	})
}
