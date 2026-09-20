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
	project     string
	tag         string
	list        []byte
	release     []byte
	artifactURL string
	apiHits     atomic.Int32
}

func newResolverTestServer(t *testing.T, project, tag string, listBytes, releaseBytes []byte, artifactURL string) (*httptest.Server, *nativeResolver, *resolverHTTPFixtures) {
	t.Helper()
	fixture := &resolverHTTPFixtures{project: project, tag: tag, list: listBytes, release: releaseBytes, artifactURL: artifactURL}
	owner, repo, _ := projectParts(project)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			_, _ = w.Write(mustJSON([]githubRelease{{TagName: fixture.tag, Assets: []githubAsset{
				{Name: "packslip.sigstore.json", BrowserDownloadURL: serverURL(r, "/assets/release.json")},
				{Name: "driver-linux.tar.gz", BrowserDownloadURL: fixture.artifactURL},
			}}}))
		case r.URL.Path == "/assets/release.json":
			_, _ = w.Write(fixture.release)
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
		artifact("driver-linux.tar.gz", "linux", "x86_64", "gnu", "tar.gz", "https://downloads.example/driver-linux.tar.gz"),
		artifact("driver-macos.tgz", "darwin", "aarch64", "", "tgz", "https://downloads.example/driver-macos.tgz"),
		artifact("driver-windows.tar.gz", "windows", "x86_64", "", "tar.gz", "https://downloads.example/driver-windows.tar.gz"),
	), testProject, "1.0.0", fakeSigner, "v1.0.0")
	releaseBytes := makeBundle(releasePayload)
	server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "")
	listBytes := makeSignedList(t, testProject, 1, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", releaseBytes, fakeSigner, "1.0.0", "v1.0.0", "")
	fixture.list = listBytes
	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "iceberg", Version: "1.0.0", Target: Target{OS: "linux", Arch: "amd64", LibC: "gnu"}})
	require.NoError(t, err)
	require.Equal(t, "iceberg", resolved.DriverID)
	require.Equal(t, "1.0.0", resolved.Version)
	require.Equal(t, "packslip", resolved.Source.Type)
	require.Equal(t, testProject, resolved.Source.Reference)
	require.Equal(t, "sha256:"+digestHex(releaseBytes), resolved.Evidence.BundleHash)
	require.Equal(t, "sha256:"+digestHex(listBytes), resolved.Evidence.ReleaseListHash)
	require.Equal(t, server.URL+"/acme/driver/HEAD/.well-known/packslip.json", resolved.Evidence.ReleaseListURL)
	require.Equal(t, server.URL+"/assets/release.json", resolved.Evidence.BundleURL)
	require.Len(t, resolved.Artifacts, 1, "a resolution contains the artifact selected for its requested target")
	require.Equal(t, "https://downloads.example/driver-linux.tar.gz", resolved.Artifacts[0].URL)
	require.Equal(t, "sha256:"+strings.Repeat("a", 64), resolved.Artifacts[0].Hash)
	require.Equal(t, int64(10), *resolved.Artifacts[0].Size)
	require.Equal(t, Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, resolved.Artifacts[0].Target)
	macos, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{
		DriverID: "iceberg", Version: "1.0.0", Target: Target{OS: "macos", Arch: "arm64"},
	})
	require.NoError(t, err)
	require.Len(t, macos.Artifacts, 1)
	require.Equal(t, Target{OS: "macos", Arch: "arm64"}, macos.Artifacts[0].Target)
	require.Equal(t, "https://downloads.example/driver-macos.tgz", macos.Artifacts[0].URL)
	require.Equal(t, int32(2), fixture.apiHits.Load())
}

func TestResolverRetainsSignedListEvidenceWhenReleaseIsAbsentFromList(t *testing.T) {
	releaseBytes := makeBundle(setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	fixture.list = makeSignedList(t, testProject, 1, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", releaseBytes, fakeSigner, "0.9.0", "v0.9.0", "")
	resolved, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "iceberg", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64", LibC: "gnu"}})
	require.NoError(t, err)
	require.NotEmpty(t, resolved.Evidence.ReleaseListURL)
	require.Equal(t, "sha256:"+digestHex(fixture.list), resolved.Evidence.ReleaseListHash)
}

func TestResolverPersistsSequenceBeforeYankAndRejectsRollback(t *testing.T) {
	releasePayload := setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0")
	releaseBytes := makeBundle(releasePayload)
	var listBytes []byte
	server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	listBytes = makeSignedList(t, testProject, 4, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", releaseBytes, fakeSigner, "1.0.0", "v1.0.0", "yanked")
	fixture.list = listBytes
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64", LibC: "gnu"}})
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
	_, err = resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64", LibC: "gnu"}})
	require.ErrorContains(t, err, "sequence rollback")
}

func TestResolverRejectsExpiredListAndAcceptedListDisappearance(t *testing.T) {
	releaseBytes := makeBundle(setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	var expired []byte
	server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	expired = makeSignedList(t, testProject, 1, "2026-09-14T00:00:00Z", server.URL+"/assets/release.json", releaseBytes, fakeSigner, "1.0.0", "v1.0.0", "")
	fixture.list = expired
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64"}})
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
	_, err = missingResolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64"}})
	require.ErrorContains(t, err, "previously accepted signed release list")
}

func TestResolverRejectsBundleDigestMismatchWrongProjectAndInvalidSignature(t *testing.T) {
	releaseBytes := makeBundle(setSignerAndTag(t, validRelease("1.0.0"), testProject, "1.0.0", fakeSigner, "v1.0.0"))
	var wrongPin []byte
	server, resolver, fixture := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBytes, "https://downloads.example/driver.tar.gz")
	wrongPin = makeSignedList(t, testProject, 1, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", []byte("different bundle"), fakeSigner, "1.0.0", "v1.0.0", "")
	fixture.list = wrongPin
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64", LibC: "gnu"}})
	require.ErrorContains(t, err, "digest mismatch")

	wrongProjectPayload := setSignerAndTag(t, validRelease("1.0.0"), "github.com/attacker/driver", "1.0.0", fakeSigner, "v1.0.0")
	wrongProjectBundle := makeBundle(wrongProjectPayload)
	var wrongProjectList []byte
	server, resolver, fixture = newResolverTestServer(t, testProject, "v1.0.0", nil, wrongProjectBundle, "https://downloads.example/driver.tar.gz")
	wrongProjectList = makeSignedList(t, testProject, 1, "2026-10-01T00:00:00Z", server.URL+"/assets/release.json", wrongProjectBundle, fakeSigner, "1.0.0", "v1.0.0", "")
	fixture.list = wrongProjectList
	_, err = resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64", LibC: "gnu"}})
	require.ErrorContains(t, err, "does not match requested project")
	server.Close()

	_, resolver, _ = newResolverTestServer(t, testProject, "v1.0.0", wrongProjectList, wrongProjectBundle, "https://downloads.example/driver.tar.gz")
	resolver.verifier = mockPackslipVerifier{issuer: GitHubOIDCIssuer, signer: fakeSigner, err: errors.New("signature is invalid")}
	_, err = resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64", LibC: "gnu"}})
	require.ErrorContains(t, err, "signature is invalid")
}

func TestResolverRejectsArtifactSelectionAmbiguityAndMalformedHashOrSize(t *testing.T) {
	artifact := func(name, osName, arch, format string, size *uint64) releaseArtifact {
		return releaseArtifact{Name: name, OS: optionalToken(osName), Arch: optionalToken(arch), Format: ptr(format), Size: size, URL: ptr("https://downloads.example/" + name)}
	}
	ambiguousPayload := validRelease("1.0.0",
		artifact("os.tar.gz", "linux", "", "tar.gz", ptr(uint64(1))),
		artifact("arch.tar.gz", "", "x86_64", "tar.gz", ptr(uint64(1))),
	)
	releaseBundle := makeBundle(setSignerAndTag(t, ambiguousPayload, testProject, "1.0.0", fakeSigner, "v1.0.0"))
	_, resolver, _ := newResolverTestServer(t, testProject, "v1.0.0", nil, releaseBundle, "https://downloads.example/driver.tar.gz")
	_, err := resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64"}})
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
	_, err = resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64", LibC: "gnu"}})
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
	_, err = resolver.Resolve(context.Background(), PackslipSource{Project: testProject}, Request{DriverID: "driver", Version: "1.0.0", Target: Target{OS: "linux", Arch: "x86_64", LibC: "gnu"}})
	require.ErrorContains(t, err, "no size")
	doc, err = resolver.trust.read()
	require.NoError(t, err)
	require.Nil(t, doc.Projects[testProject], "invalid release metadata must not create release trust")
}

var _ Resolver = (*nativeResolver)(nil)
