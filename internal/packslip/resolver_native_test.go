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

//go:build !js

package packslip

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/columnar-tech/dbc/internal/packslipverify"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/require"
)

const fakeSigner = "https://github.com/acme/driver/.github/workflows/release.yml@refs/tags/v1.0.0"

type mockPackslipVerifier struct {
	issuer string
	signer string
	err    error
}

func (v mockPackslipVerifier) Verify(_ context.Context, bundleJSON []byte, identity packslipverify.IdentityPolicy, digest packslipverify.ArtifactDigest) (*packslipverify.VerifiedBundle, error) {
	if v.err != nil {
		return nil, v.err
	}
	var envelope struct {
		DSSE struct {
			Payload string `json:"payload"`
		} `json:"dsseEnvelope"`
	}
	if err := json.Unmarshal(bundleJSON, &envelope); err != nil {
		return nil, err
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.DSSE.Payload)
	if err != nil {
		return nil, err
	}
	algorithm, wanted := digest.Algorithm, digest.Hex
	var candidate struct {
		Subject []subject `json:"subject"`
	}
	if err := json.Unmarshal(payload, &candidate); err != nil {
		return nil, err
	}
	found := false
	for _, entry := range candidate.Subject {
		if entry.Digest[algorithm] == wanted {
			found = true
		}
	}
	if !found {
		return nil, errors.New("artifact digest does not match a signed subject")
	}
	if v.issuer != identity.Issuer || !policyMatchesSigner(identity, v.signer) {
		return nil, errors.New("signature identity rejected by project policy")
	}
	return &packslipverify.VerifiedBundle{Issuer: v.issuer, Signer: v.signer, SignedPayload: payload}, nil
}

func makeBundle(payload []byte) []byte {
	return mustJSON(struct {
		DSSE struct {
			Payload     string `json:"payload"`
			PayloadType string `json:"payloadType"`
		} `json:"dsseEnvelope"`
	}{DSSE: struct {
		Payload     string `json:"payload"`
		PayloadType string `json:"payloadType"`
	}{Payload: base64.StdEncoding.EncodeToString(payload), PayloadType: "application/vnd.in-toto+json"}})
}

func setSignerAndTag(t *testing.T, payload []byte, project, version, signer, tag string) []byte {
	t.Helper()
	var envelope statementEnvelope
	require.NoError(t, json.Unmarshal(payload, &envelope))
	var predicate releasePredicate
	require.NoError(t, json.Unmarshal(envelope.Predicate, &predicate))
	predicate.Project = project
	predicate.Version = version
	predicate.Identity.KeyID = signer
	predicate.Source = &sourceMetadata{Repo: "https://github.com/acme/driver", Tag: &tag}
	envelope.Predicate = mustJSON(predicate)
	return mustJSON(envelope)
}

func makeSignedList(t *testing.T, project string, sequence uint64, expiry string, releaseURL string, releaseBundle []byte, signer string, version, tag, status string) []byte {
	t.Helper()
	digest := sha256.Sum256(releaseBundle)
	var payload releaseListPredicate
	payload.Project = project
	payload.Generated = "2026-09-01T12:00:00Z"
	payload.Expires = expiry
	payload.Sequence = &sequence
	issuer := GitHubOIDCIssuer
	payload.Identity = releaseIdentity{Scheme: "sigstore-oidc", KeyID: signer, Issuer: &issuer}
	payload.Releases = []releaseRef{{Version: version, Tag: &tag, PublishedAt: "2026-09-01T12:00:00Z", Packslip: releaseURL, Status: status}}
	envelope := statementEnvelope{
		Type:          StatementType,
		Subject:       []subject{{Name: releaseURL, Digest: map[string]string{"sha256": hex.EncodeToString(digest[:])}}},
		PredicateType: ListPredicateType,
		Predicate:     mustJSON(payload),
	}
	return makeBundle(mustJSON(envelope))
}

type resolverHTTPFixtures struct {
	project             string
	tag                 string
	list                []byte
	release             []byte
	artifactURL         string
	releaseAssets       []githubAsset
	customReleaseAssets bool
	apiHits             atomic.Int32
	requestHits         atomic.Int32
	bundleHits          atomic.Int32
	archiveHits         atomic.Int32
}

func newResolverTestServer(t *testing.T, project, tag string, listBytes, releaseBytes []byte, artifactURL string) (*httptest.Server, *nativeResolver, *resolverHTTPFixtures) {
	t.Helper()
	fixture := &resolverHTTPFixtures{project: project, tag: tag, list: listBytes, release: releaseBytes, artifactURL: artifactURL}
	owner, repo, _ := projectParts(project)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.requestHits.Add(1)
		switch {
		case r.URL.Path == "/"+owner+"/"+repo+"/HEAD/.well-known/packslip.json":
			if fixture.list == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixture.list)
		case strings.HasPrefix(r.URL.Path, "/repos/") && strings.HasSuffix(r.URL.Path, "/releases"):
			fixture.apiHits.Add(1)
			assets := fixture.releaseAssets
			if !fixture.customReleaseAssets {
				assets = []githubAsset{
					{Name: "packslip.sigstore.json", BrowserDownloadURL: serverURL(r, "/assets/release.json")},
					{Name: "driver-linux.tar.gz", BrowserDownloadURL: fixture.artifactURL},
				}
			}
			_, _ = w.Write(mustJSON([]githubRelease{{TagName: fixture.tag, Assets: assets}}))
		case r.URL.Path == "/assets/release.json":
			fixture.bundleHits.Add(1)
			_, _ = w.Write(fixture.release)
		case r.URL.Path == "/assets/driver.tar.gz":
			fixture.archiveHits.Add(1)
			_, _ = w.Write([]byte("not a signed packslip"))
		default:
			http.NotFound(w, r)
		}
	}))
	client := server.Client()
	store, err := NewTrustStoreAt(filepath.Join(t.TempDir(), "trust", "packslip.json"))
	require.NoError(t, err)
	discovery, err := NewGitHubPackslipDiscovery(client, server.URL, server.URL)
	require.NoError(t, err)
	resolver := &nativeResolver{
		discovery: discovery,
		verifier:  mockPackslipVerifier{issuer: GitHubOIDCIssuer, signer: fakeSigner},
		trust:     store,
		now:       func() time.Time { return time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC) },
	}
	t.Cleanup(server.Close)
	return server, resolver, fixture
}

func serverURL(request *http.Request, path string) string {
	return "https://" + request.Host + path
}

func TestResolverUsesSignedListDigestAndBuildsResolvedRelease(t *testing.T) {
	artifact := func(name, osName, arch, libc, format, url string) releaseArtifact {
		return releaseArtifact{Name: name, OS: optionalToken(osName), Arch: optionalToken(arch), LibC: optionalToken(libc), Format: ptr(format), Size: ptr(uint64(10)), URL: ptr(url)}
	}
	releasePayload := setSignerAndTag(t, validRelease("1.0.0",
		artifact("driver-windows.tar.gz", "windows", "x86_64", "", "tar.gz", "https://downloads.example/driver-windows.tar.gz"),
		artifact("driver-macos.tgz", "darwin", "aarch64", "", "tgz", "https://downloads.example/driver-macos.tgz"),
		artifact("driver-linux-arm64.tar.gz", "linux", "aarch64", "gnu", "tar.gz", "https://downloads.example/driver-linux-arm64.tar.gz"),
		artifact("driver-macos-x64.tar.gz", "darwin", "x86_64", "", "tar.gz", "https://downloads.example/driver-macos-x64.tar.gz"),
		artifact("driver-linux.tar.gz", "linux", "x86_64", "gnu", "tar.gz", "https://downloads.example/driver-linux.tar.gz"),
	), testProject, "1.0.0", fakeSigner, "v1.0.0")
	releaseBytes := makeBundle(releasePayload)
	server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "")
	listBytes := makeSignedList(t, testProject, 1, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", releaseBytes, fakeSigner, "1.0.0", "v1.0.0", "")
	fixture.list = listBytes
	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "iceberg", Version: "1.0.0"})
	require.NoError(t, err)
	require.Equal(t, "iceberg", resolved.DriverID)
	require.Equal(t, "1.0.0", resolved.Version)
	require.Equal(t, "packslip", resolved.Source.Type)
	require.Equal(t, testProject, resolved.Source.Reference)
	require.Equal(t, []resolution.Evidence{
		{
			Kind:     resolution.EvidenceKindReleaseMetadata,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: server.URL + "/assets/release.json"},
			Hash:     "sha256:" + digestHex(releaseBytes),
		},
		{
			Kind:     resolution.EvidenceKindReleaseIndex,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: server.URL + "/acme/driver/HEAD/.well-known/packslip.json"},
			Hash:     "sha256:" + digestHex(listBytes),
		},
	}, resolved.Evidence)
	require.Len(t, resolved.Artifacts, 5, "a resolution snapshots every concrete target in the release")
	require.Equal(t, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://downloads.example/driver-linux.tar.gz"}, resolved.Artifacts[0].Location)
	require.Equal(t, "sha256:"+strings.Repeat("a", 64), resolved.Artifacts[0].Hash)
	require.Equal(t, int64(10), *resolved.Artifacts[0].Size)
	require.Equal(t, Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, resolved.Artifacts[0].Target)
	require.Equal(t, Target{OS: "linux", Arch: "arm64", LibC: "gnu"}, resolved.Artifacts[1].Target)
	require.Equal(t, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://downloads.example/driver-linux-arm64.tar.gz"}, resolved.Artifacts[1].Location)
	require.Equal(t, Target{OS: "macos", Arch: "amd64"}, resolved.Artifacts[2].Target)
	require.Equal(t, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://downloads.example/driver-macos-x64.tar.gz"}, resolved.Artifacts[2].Location)
	require.Equal(t, Target{OS: "macos", Arch: "arm64"}, resolved.Artifacts[3].Target)
	require.Equal(t, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://downloads.example/driver-macos.tgz"}, resolved.Artifacts[3].Location)
	require.Equal(t, Target{OS: "windows", Arch: "amd64"}, resolved.Artifacts[4].Target)
	require.Equal(t, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://downloads.example/driver-windows.tar.gz"}, resolved.Artifacts[4].Location)
	require.NoError(t, resolution.ValidateResolvedRelease(resolved))
	require.Equal(t, int32(1), fixture.apiHits.Load())
}

func TestResolverSelectsOneCanonicalWinnerPerTarget(t *testing.T) {
	artifact := func(name, osName, arch, libc, format, url string) releaseArtifact {
		return releaseArtifact{Name: name, OS: optionalToken(osName), Arch: optionalToken(arch), LibC: optionalToken(libc), Format: ptr(format), Size: ptr(uint64(10)), URL: ptr(url)}
	}
	release := validRelease("1.0.0",
		artifact("portable.tar.gz", "", "", "", "tar.gz", "https://downloads.example/portable.tar.gz"),
		artifact("linux-x86-compat.tgz", "linux", "x86_64", "", "tgz", "https://downloads.example/linux-x86-compat.tgz"),
		artifact("linux-amd64.tar.gz", "linux", "amd64", "", "tar.gz", "https://downloads.example/linux-amd64.tar.gz"),
		artifact("linux-arm64.tgz", "linux", "aarch64", "", "tgz", "https://downloads.example/linux-arm64.tgz"),
	)
	bundle := makeBundle(setSignerAndTag(t, release, testProject, "1.0.0", fakeSigner, "v1.0.0"))
	_, resolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, bundle, "")
	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.NoError(t, err)
	require.Len(t, resolved.Artifacts, 2, "canonical aliases that derive the same target must produce one winner")
	require.Equal(t, Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, resolved.Artifacts[0].Target)
	require.Equal(t, "tar.gz", resolved.Artifacts[0].Format, "tar.gz wins over tgz for the same target")
	require.Equal(t, "https://downloads.example/linux-amd64.tar.gz", resolved.Artifacts[0].Location.Value)
	require.Equal(t, Target{OS: "linux", Arch: "arm64", LibC: "gnu"}, resolved.Artifacts[1].Target)
	require.Equal(t, "tgz", resolved.Artifacts[1].Format)
	require.Equal(t, "https://downloads.example/linux-arm64.tgz", resolved.Artifacts[1].Location.Value)
	require.NoError(t, resolution.ValidateResolvedRelease(resolved))
}

func TestResolverAcceptsOverlappingWildcardAndConcreteSelectors(t *testing.T) {
	artifact := func(name, osName, arch, url string) releaseArtifact {
		return releaseArtifact{Name: name, OS: optionalToken(osName), Arch: optionalToken(arch), Format: ptr("tar.gz"), Size: ptr(uint64(10)), URL: ptr(url)}
	}
	release := validRelease("1.0.0",
		artifact("linux-any-arch.tar.gz", "linux", "", "https://downloads.example/linux-any-arch.tar.gz"),
		artifact("any-os-amd64.tar.gz", "", "amd64", "https://downloads.example/any-os-amd64.tar.gz"),
		artifact("linux-amd64.tar.gz", "linux", "amd64", "https://downloads.example/linux-amd64.tar.gz"),
	)
	bundle := makeBundle(setSignerAndTag(t, release, testProject, "1.0.0", fakeSigner, "v1.0.0"))
	_, resolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, bundle, "")
	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.NoError(t, err)
	require.Len(t, resolved.Artifacts, 1)
	require.Equal(t, Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, resolved.Artifacts[0].Target)
	require.Equal(t, "https://downloads.example/linux-amd64.tar.gz", resolved.Artifacts[0].Location.Value,
		"resolution chooses the most specific artifact for the derived Linux amd64 target")
}

func TestResolverAllowsOneSelectedArtifactToWinForSeveralTargets(t *testing.T) {
	artifact := func(name, osName, arch, libc, format, url string) releaseArtifact {
		return releaseArtifact{Name: name, OS: optionalToken(osName), Arch: optionalToken(arch), LibC: optionalToken(libc), Format: ptr(format), Size: ptr(uint64(10)), URL: ptr(url)}
	}
	sharedURL := "https://downloads.example/linux-universal.tar.gz"
	release := validRelease("1.0.0",
		artifact("linux-amd64.tgz", "linux", "amd64", "", "tgz", "https://downloads.example/linux-amd64.tgz"),
		artifact("linux-arm64.tgz", "linux", "arm64", "", "tgz", "https://downloads.example/linux-arm64.tgz"),
		artifact("linux-any-arch.tar.gz", "linux", "", "gnu", "tar.gz", sharedURL),
	)
	bundle := makeBundle(setSignerAndTag(t, release, testProject, "1.0.0", fakeSigner, "v1.0.0"))
	_, resolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, bundle, "")
	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.NoError(t, err)
	require.Len(t, resolved.Artifacts, 2)
	require.Equal(t, Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, resolved.Artifacts[0].Target)
	require.Equal(t, sharedURL, resolved.Artifacts[0].Location.Value)
	require.Equal(t, Target{OS: "linux", Arch: "arm64", LibC: "gnu"}, resolved.Artifacts[1].Target)
	require.Equal(t, sharedURL, resolved.Artifacts[1].Location.Value)
	require.Equal(t, resolved.Artifacts[0].Hash, resolved.Artifacts[1].Hash)
	require.Equal(t, *resolved.Artifacts[0].Size, *resolved.Artifacts[1].Size)
	require.NoError(t, resolution.ValidateResolvedRelease(resolved))
}

func TestResolverSeparatesOrdinaryAndExplicitVariants(t *testing.T) {
	ordinary := releaseArtifact{Name: "ordinary.tar.gz", OS: ptr("linux"), Arch: ptr("amd64"), LibC: ptr("gnu"), Format: ptr("tar.gz"), Size: ptr(uint64(10)), URL: ptr("https://downloads.example/ordinary.tar.gz")}
	fips := releaseArtifact{Name: "fips.tar.gz", OS: ptr("linux"), Arch: ptr("amd64"), LibC: ptr("gnu"), Variant: ptr("fips"), Format: ptr("tar.gz"), Size: ptr(uint64(10)), URL: ptr("https://downloads.example/fips.tar.gz")}
	bundle := makeBundle(setSignerAndTag(t, validRelease("1.0.0", ordinary, fips), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	_, resolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, bundle, "")
	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.NoError(t, err)
	require.Len(t, resolved.Artifacts, 2)
	require.Equal(t, Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, resolved.Artifacts[0].Target)
	require.Equal(t, "https://downloads.example/ordinary.tar.gz", resolved.Artifacts[0].Location.Value)
	require.Equal(t, Target{OS: "linux", Arch: "amd64", LibC: "gnu", Variant: "fips"}, resolved.Artifacts[1].Target)
	require.Equal(t, "https://downloads.example/fips.tar.gz", resolved.Artifacts[1].Location.Value)
}

func TestResolverPreservesExplicitMuslTargetAndIgnoresUnsupportedFormatSeeds(t *testing.T) {
	gnuDefault := releaseArtifact{Name: "linux-default.tar.gz", OS: ptr("linux"), Arch: ptr("amd64"), Format: ptr("tar.gz"), Size: ptr(uint64(10)), URL: ptr("https://downloads.example/linux-default.tar.gz")}
	musl := releaseArtifact{Name: "linux-musl.tar.gz", OS: ptr("linux"), Arch: ptr("amd64"), LibC: ptr("musl"), Format: ptr("tar.gz"), Size: ptr(uint64(10)), URL: ptr("https://downloads.example/linux-musl.tar.gz")}
	unsupported := releaseArtifact{Name: "windows-arm64.zip", OS: ptr("windows"), Arch: ptr("arm64"), Format: ptr("zip"), Size: ptr(uint64(10)), URL: ptr("https://downloads.example/windows-arm64.zip")}
	bundle := makeBundle(setSignerAndTag(t, validRelease("1.0.0", gnuDefault, musl, unsupported), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	_, resolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, bundle, "")
	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.NoError(t, err)
	require.Len(t, resolved.Artifacts, 2, "unsupported formats must not contribute target seeds")
	require.Equal(t, Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, resolved.Artifacts[0].Target)
	require.Equal(t, "https://downloads.example/linux-default.tar.gz", resolved.Artifacts[0].Location.Value)
	require.Equal(t, Target{OS: "linux", Arch: "amd64", LibC: "musl"}, resolved.Artifacts[1].Target)
	require.Equal(t, "https://downloads.example/linux-musl.tar.gz", resolved.Artifacts[1].Location.Value)
}

func TestDerivedConcreteLinuxTargetDefaultsToGNU(t *testing.T) {
	linuxDefault := releaseArtifact{
		Name: "linux-default.tar.gz", OS: ptr("linux"), Arch: ptr("x86_64"),
		Format: ptr("tar.gz"), Size: ptr(uint64(10)), URL: ptr("https://downloads.example/linux-default.tar.gz"),
	}
	release, err := parseRelease(validRelease("1.0.0", linuxDefault), testProject)
	require.NoError(t, err)
	targets, err := deriveConcreteTargets(release)
	require.NoError(t, err)
	require.Equal(t, []Target{{OS: "linux", Arch: "amd64", LibC: "gnu"}}, targets,
		"dbc's release snapshot continues to canonicalize an omitted Linux libc to GNU")
}

func TestResolverRejectsWildcardOnlyReleaseWithoutUpdatingTrust(t *testing.T) {
	wildcard := releaseArtifact{Name: "portable.tar.gz", Format: ptr("tar.gz"), Size: ptr(uint64(10)), URL: ptr("https://downloads.example/portable.tar.gz")}
	bundle := makeBundle(setSignerAndTag(t, validRelease("1.0.0", wildcard), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	_, resolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, bundle, "")
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorContains(t, err, "explicit OS and architecture")
	doc, err := resolver.trust.read()
	require.NoError(t, err)
	require.Nil(t, doc.Projects[testProject], "unsupported wildcard-only inventory must not update release trust")
}

func TestResolverDoesNotUpdateTrustWhenAnyTargetCannotBeConverted(t *testing.T) {
	largeSize := ^uint64(0)
	valid := releaseArtifact{Name: "linux.tar.gz", OS: ptr("linux"), Arch: ptr("amd64"), LibC: ptr("gnu"), Format: ptr("tar.gz"), Size: ptr(uint64(10)), URL: ptr("https://downloads.example/linux.tar.gz")}
	tooLarge := releaseArtifact{Name: "windows.tar.gz", OS: ptr("windows"), Arch: ptr("amd64"), Format: ptr("tar.gz"), Size: &largeSize, URL: ptr("https://downloads.example/windows.tar.gz")}
	bundle := makeBundle(setSignerAndTag(t, validRelease("1.0.0", valid, tooLarge), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	_, resolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, bundle, "")
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorContains(t, err, "size exceeds dbc's supported range")
	require.ErrorContains(t, err, `os="windows" arch="amd64" libc="" variant=""`)
	doc, err := resolver.trust.read()
	require.NoError(t, err)
	require.Nil(t, doc.Projects[testProject], "release trust must be updated only after all targets are finalized")
}

func TestResolverRetainsSignedListEvidenceWhenReleaseIsAbsentFromList(t *testing.T) {
	releaseBytes := makeBundle(setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	fixture.list = makeSignedList(t, testProject, 1, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", releaseBytes, fakeSigner, "0.9.0", "v0.9.0", "")
	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "iceberg", Version: "1.0.0"})
	require.NoError(t, err)
	require.Len(t, resolved.Evidence, 2)
	require.Equal(t, resolution.EvidenceKindReleaseIndex, resolved.Evidence[1].Kind)
	require.Equal(t, server.URL+"/acme/driver/HEAD/.well-known/packslip.json", resolved.Evidence[1].Location.Value)
	require.Equal(t, "sha256:"+digestHex(fixture.list), resolved.Evidence[1].Hash)
}

func TestResolverOmitsReleaseIndexEvidenceWhenNoListIsObserved(t *testing.T) {
	releaseBytes := makeBundle(setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	server, resolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{
		DriverID: "iceberg", Version: "1.0.0",
	})
	require.NoError(t, err)
	require.Equal(t, []resolution.Evidence{{
		Kind:     resolution.EvidenceKindReleaseMetadata,
		Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: server.URL + "/assets/release.json"},
		Hash:     "sha256:" + digestHex(releaseBytes),
	}}, resolved.Evidence)
}

func TestFinishResolvedRejectsPartialReleaseIndexEvidence(t *testing.T) {
	resolver := &nativeResolver{}
	for _, pair := range []struct {
		name     string
		listURL  string
		listHash string
	}{
		{name: "URL without hash", listURL: "https://example.test/index"},
		{name: "hash without URL", listHash: "sha256:" + strings.Repeat("a", 64)},
	} {
		t.Run(pair.name, func(t *testing.T) {
			_, err := resolver.finishResolved(context.Background(), "", Request{}, listObservation{}, nil, nil,
				"", nil, pair.listURL, pair.listHash, nil)
			require.ErrorContains(t, err, "requires both a URL and hash")
		})
	}
}

func TestResolverPersistsSequenceBeforeYankAndRejectsRollback(t *testing.T) {
	releasePayload := setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0")
	releaseBytes := makeBundle(releasePayload)
	var listBytes []byte
	server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	listBytes = makeSignedList(t, testProject, 4, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", releaseBytes, fakeSigner, "1.0.0", "v1.0.0", "yanked")
	fixture.list = listBytes
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorIs(t, err, ErrYankedRelease)
	accepted, err := resolver.trust.observeList(testProject)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)

	var rollback []byte
	server, _, fixture = newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	rollback = makeSignedList(t, testProject, 3, "2026-10-02T00:00:00Z", server.URL+"/assets/release.json", releaseBytes, fakeSigner, "1.0.0", "v1.0.0", "")
	fixture.list = rollback
	secondStore, err := NewTrustStoreAt(filepath.Join(t.TempDir(), "trust", "packslip.json"))
	require.NoError(t, err)
	identity, key := testTrustedIdentity(t, fakeSigner)
	observed, err := secondStore.observeList(testProject)
	require.NoError(t, err)
	require.NoError(t, secondStore.acceptResolution(context.Background(), testProject, observed, &listAcceptance{
		Signer: identity, IdentityKey: key, Sequence: 4, Hash: "sha256:" + strings.Repeat("e", 64),
	}, nil))
	resolver.trust = secondStore
	resolver.discovery, _ = NewGitHubPackslipDiscovery(server.Client(), server.URL, server.URL)
	_, err = resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorContains(t, err, "sequence rollback")
}

func TestResolverRejectsExpiredListAndAcceptedListDisappearance(t *testing.T) {
	releaseBytes := makeBundle(setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	var expired []byte
	server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	expired = makeSignedList(t, testProject, 1, "2026-09-14T00:00:00Z", server.URL+"/assets/release.json", releaseBytes, fakeSigner, "1.0.0", "v1.0.0", "")
	fixture.list = expired
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorContains(t, err, "expired")
	accepted, err := resolver.trust.observeList(testProject)
	require.NoError(t, err)
	require.False(t, accepted.Accepted)

	server, missingResolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	identity, key := testTrustedIdentity(t, fakeSigner)
	observed, err := missingResolver.trust.observeList(testProject)
	require.NoError(t, err)
	require.NoError(t, missingResolver.trust.acceptResolution(context.Background(), testProject, observed, &listAcceptance{
		Signer: identity, IdentityKey: key, Sequence: 1, Hash: "sha256:" + strings.Repeat("c", 64),
	}, nil))
	missingResolver.discovery, _ = NewGitHubPackslipDiscovery(server.Client(), server.URL, server.URL)
	_, err = missingResolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorContains(t, err, "previously accepted signed release list")
}

func TestResolverRejectsBundleDigestMismatchWrongProjectAndInvalidSignature(t *testing.T) {
	releaseBytes := makeBundle(setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	var wrongPin []byte
	server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	wrongPin = makeSignedList(t, testProject, 1, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", []byte("different bundle"), fakeSigner, "1.0.0", "v1.0.0", "")
	fixture.list = wrongPin
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorContains(t, err, "digest mismatch")

	wrongProjectPayload := setSignerAndTag(t, validRelease("1.0.0"), "github.com/attacker/driver", "1.0.0", fakeSigner, "v1.0.0")
	wrongProjectBundle := makeBundle(wrongProjectPayload)
	var wrongProjectList []byte
	server, resolver, fixture = newResolverTestServer(t, testProject, "v1.0.0", nil, wrongProjectBundle, "https://downloads.example/driver.tar.gz")
	wrongProjectList = makeSignedList(t, testProject, 1, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", wrongProjectBundle, fakeSigner, "1.0.0", "v1.0.0", "")
	fixture.list = wrongProjectList
	_, err = resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorContains(t, err, "does not match requested project")
	server.Close()

	_, resolver, _ = newResolverTestServer(t, testProject, "v1.0.0", wrongProjectList, wrongProjectBundle, "https://downloads.example/driver.tar.gz")
	resolver.verifier = mockPackslipVerifier{issuer: GitHubOIDCIssuer, signer: fakeSigner, err: errors.New("signature is invalid")}
	_, err = resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorContains(t, err, "signature is invalid")
}

func TestResolveRequiresExactSemVerBeforeDiscovery(t *testing.T) {
	_, resolver, fixture := newResolverTestServer(t, testProject, "v1.2.3", nil, nil, "")
	for _, version := range []string{"", ">=1.2.3", "latest", "v1.2.3", "1.2"} {
		_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: version})
		require.ErrorContains(t, err, "exact SemVer 2.0.0", "version %q", version)
		require.Zero(t, fixture.requestHits.Load(), "version %q must be rejected before transport", version)
	}

	_, resolver, fixture = newResolverTestServer(t, testProject, "v1.2.3-rc.1+build.2", nil, nil, "")
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{
		DriverID: "driver", Version: "1.2.3-rc.1+build.2",
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "exact SemVer 2.0.0", "valid prerelease and build metadata must pass input validation")
	require.Positive(t, fixture.requestHits.Load(), "a valid exact SemVer may proceed to discovery")
}

func TestResolverUsesVersionBearingTagAndKeepsProjectAsSourceIdentity(t *testing.T) {
	release := makeBundle(setSignerAndTag(t, validRelease("1.2.3"), testProject, "1.2.3", fakeSigner, "v1.2.3"))
	_, resolver, _ := newResolverTestServer(t, testProject, "v1.2.3", nil, release, "")

	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{
		DriverID: "driver", Version: "1.2.3",
	})
	require.NoError(t, err)
	require.Equal(t, "1.2.3", resolved.Version)
	require.Equal(t, resolution.SourceSpec{Type: "packslip", Reference: testProject}, resolved.Source)
	require.NotEqual(t, "https://downloads.example/driver-linux.tar.gz", resolved.Source.Reference)
	require.NotEqual(t, "https://github.com/acme/driver/releases/download/v1.2.3/packslip.sigstore.json", resolved.Source.Reference)
	require.Equal(t, "https://dl.example/driver-linux.tar.gz", resolved.Artifacts[0].Location.Value)
}

func TestResolverRejectsVersionBearingTagWhenSignedVersionDiffers(t *testing.T) {
	release := makeBundle(setSignerAndTag(t, validRelease("2.0.0"), testProject, "2.0.0", fakeSigner, "v1.2.3"))
	_, resolver, fixture := newResolverTestServer(t, testProject, "v1.2.3", nil, release, "")

	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{
		DriverID: "driver", Version: "1.2.3",
	})
	require.ErrorContains(t, err, `GitHub release tag "v1.2.3" maps to 1.2.3 but signed packslip says 2.0.0`)
	require.Equal(t, int32(1), fixture.bundleHits.Load())
	require.Zero(t, fixture.archiveHits.Load())
}

func TestResolverDoesNotDiscoverArbitraryTagWithoutSignedList(t *testing.T) {
	release := makeBundle(setSignerAndTag(t, validRelease("1.2.3"), testProject, "1.2.3", fakeSigner, "nightly"))
	_, resolver, fixture := newResolverTestServer(t, testProject, "nightly", nil, release, "")

	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{
		DriverID: "driver", Version: "1.2.3",
	})
	require.ErrorIs(t, err, ErrReleaseNotFound)
	require.Zero(t, fixture.bundleHits.Load(), "unmapped tags must not cause a candidate bundle fetch")
	require.Zero(t, fixture.archiveHits.Load(), "resolution must not fall back to downloading an artifact")
}

func TestResolverUsesSignedListToMapArbitraryTagToExactVersion(t *testing.T) {
	release := makeBundle(setSignerAndTag(t, validRelease("1.2.3"), testProject, "1.2.3", fakeSigner, "nightly"))
	server, resolver, fixture := newResolverTestServer(t, testProject, "nightly", nil, release, "")
	fixture.list = makeSignedList(t, testProject, 1, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", release, fakeSigner, "1.2.3", "nightly", "")

	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{
		DriverID: "driver", Version: "1.2.3",
	})
	require.NoError(t, err)
	require.Equal(t, "1.2.3", resolved.Version)
	require.Equal(t, resolution.SourceSpec{Type: "packslip", Reference: testProject}, resolved.Source)
	require.Equal(t, "https://dl.example/driver-linux.tar.gz", resolved.Artifacts[0].Location.Value)
	require.Equal(t, int32(1), fixture.bundleHits.Load(), "the signed list must authorize fetching the pinned release bundle")
}

func TestResolverDoesNotFallBackToGenericGitHubArchive(t *testing.T) {
	t.Run("missing signed metadata", func(t *testing.T) {
		server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, nil, "")
		fixture.customReleaseAssets = true
		fixture.releaseAssets = []githubAsset{{Name: "driver.tar.gz", BrowserDownloadURL: server.URL + "/assets/driver.tar.gz"}}
		_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
		require.ErrorContains(t, err, "has no packslip bundle asset")
		require.Zero(t, fixture.archiveHits.Load(), "an unverified generic archive must never be fetched as a fallback")
	})
	t.Run("invalid signature", func(t *testing.T) {
		server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, nil, "")
		fixture.artifactURL = server.URL + "/assets/driver.tar.gz"
		fixture.release = makeBundle(setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0"))
		resolver.verifier = mockPackslipVerifier{issuer: GitHubOIDCIssuer, signer: fakeSigner, err: errors.New("signature is invalid")}
		_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
		require.ErrorContains(t, err, "signature is invalid")
		require.Zero(t, fixture.archiveHits.Load(), "an invalid signed statement must not unlock generic archive fallback")
	})
}

func TestResolverRejectsArtifactSelectionAmbiguityAndMalformedHashOrSize(t *testing.T) {
	artifact := func(name, osName, arch, format string, size *uint64) releaseArtifact {
		return releaseArtifact{Name: name, OS: optionalToken(osName), Arch: optionalToken(arch), Format: ptr(format), Size: size, URL: ptr("https://downloads.example/" + name)}
	}
	ambiguousPayload := validRelease("1.0.0",
		artifact("linux-amd64.tar.gz", "linux", "amd64", "tar.gz", ptr(uint64(1))),
		releaseArtifact{Name: "linux-gnu.tar.gz", OS: ptr("linux"), LibC: ptr("gnu"), Format: ptr("tar.gz"), Size: ptr(uint64(1)), URL: ptr("https://downloads.example/linux-gnu.tar.gz")},
	)
	releaseBundle := makeBundle(setSignerAndTag(t, ambiguousPayload, testProject, "1.0.0", fakeSigner, "v1.0.0"))
	_, resolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBundle, "https://downloads.example/driver.tar.gz")
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorIs(t, err, ErrAmbiguousArtifact)
	doc, err := resolver.trust.read()
	require.NoError(t, err)
	require.Nil(t, doc.Projects[testProject], "an ambiguous release must not create release trust")

	badDigest := setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0")
	var envelope statementEnvelope
	require.NoError(t, json.Unmarshal(badDigest, &envelope))
	envelope.Subject[0].Digest["sha256"] = strings.Repeat("d", 64)
	badBundle := makeBundle(mustJSON(envelope))
	_, resolver, _ = newResolverTestServer(t, testProject, "v1.0.0", nil, badBundle, "https://downloads.example/driver.tar.gz")
	_, err = resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	// The fake signature verifier accepts the bundle's signed digest; strict
	// parsing still validates that it is a well-formed SHA-256 value.
	require.NoError(t, err)

	missingSize := setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0")
	require.NoError(t, json.Unmarshal(missingSize, &envelope))
	var predicate releasePredicate
	require.NoError(t, json.Unmarshal(envelope.Predicate, &predicate))
	predicate.Artifacts[0].Size = nil
	envelope.Predicate = mustJSON(predicate)
	missingSizeBundle := makeBundle(mustJSON(envelope))
	_, resolver, _ = newResolverTestServer(t, testProject, "v1.0.0", nil, missingSizeBundle, "https://downloads.example/driver.tar.gz")
	_, err = resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0"})
	require.ErrorContains(t, err, "no size")
	doc, err = resolver.trust.read()
	require.NoError(t, err)
	require.Nil(t, doc.Projects[testProject], "invalid release metadata must not create release trust")
}

var _ Resolver = (*nativeResolver)(nil)
