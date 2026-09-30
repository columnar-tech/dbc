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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func testTrustedIdentity(t *testing.T, workflow string) (trustedIdentity, string) {
	t.Helper()
	identity, key, err := identityTrust("sigstore-oidc", GitHubOIDCIssuer, workflow)
	require.NoError(t, err)
	return identity, key
}

func testTrustStore(t *testing.T) *TrustStore {
	t.Helper()
	store, err := NewTrustStoreAt(filepath.Join(t.TempDir(), "trust", "packslip.json"))
	require.NoError(t, err)
	return store
}

func acceptTestRelease(t *testing.T, store *TrustStore, identity trustedIdentity, provenance map[string]bool, attested string) error {
	t.Helper()
	observed, err := store.observeList(testProject)
	if err != nil {
		return err
	}
	return store.acceptResolution(context.Background(), testProject, observed, nil, &releaseAcceptance{
		Signer: identity, Attested: attested, Provenance: provenance,
	})
}

func acceptTestList(t *testing.T, store *TrustStore, identity trustedIdentity, identityKey string, sequence uint64, hash string) error {
	t.Helper()
	observed, err := store.observeList(testProject)
	if err != nil {
		return err
	}
	return store.acceptResolution(context.Background(), testProject, observed, &listAcceptance{
		Signer: identity, IdentityKey: identityKey, Sequence: sequence, Hash: hash,
	}, nil)
}

func seedTestProvenance(t *testing.T, store *TrustStore, identity trustedIdentity, provenance map[string]bool) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(store.path), 0o700))
	identityCopy := identity
	doc := trustDocument{
		Version: trustStoreVersion,
		Projects: map[string]*projectTrust{
			testProject: {
				ReleaseSigner: &identityCopy,
				AttestedBy:    "vendor",
				Provenance:    provenance,
			},
		},
	}
	require.NoError(t, store.write(doc))
}

func TestTrustStoreAllowsGitHubRefChangesButRejectsWorkflowPathChanges(t *testing.T) {
	store := testTrustStore(t)
	first, _ := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/tags/v1.0.0")
	refUpdate, _ := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/tags/v1.1.0")
	pathUpdate, _ := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/publish.yml@refs/tags/v1.2.0")
	require.NoError(t, acceptTestRelease(t, store, first, map[string]bool{"linux|x86_64|gnu||tar.gz": true}, "vendor"))
	require.NoError(t, acceptTestRelease(t, store, refUpdate, map[string]bool{"linux|x86_64|gnu||tar.gz": true}, "vendor"))
	require.ErrorContains(t, acceptTestRelease(t, store, pathUpdate, map[string]bool{"linux|x86_64|gnu||tar.gz": true}, "vendor"), "release signer changed")
}

func TestGitHubSignerHostOwnerRepoAreCanonicalButWorkflowPathIsCaseSensitive(t *testing.T) {
	policy, err := githubIdentityPolicy("GitHub.COM/ACME/DRIVER/Arrow/Flight")
	require.NoError(t, err)
	require.True(t, policyMatchesSigner(policy, "https://GITHUB.COM/AcMe/Driver/.github/workflows/release.yml@refs/tags/v1"))

	canonical, canonicalKey, err := identityTrust("sigstore-oidc", GitHubOIDCIssuer, "https://github.com/acme/driver/.github/workflows/release.yml@refs/tags/v1")
	require.NoError(t, err)
	mixed, mixedKey, err := identityTrust("sigstore-oidc", GitHubOIDCIssuer, "https://GITHUB.COM/ACME/DRIVER/.github/workflows/release.yml@refs/tags/v2")
	require.NoError(t, err)
	require.Equal(t, canonicalKey, mixedKey)
	require.True(t, sameIdentity(canonical, mixed))

	changedPath, changedPathKey, err := identityTrust("sigstore-oidc", GitHubOIDCIssuer, "https://GITHUB.COM/ACME/DRIVER/.github/workflows/Release.yml@refs/tags/v3")
	require.NoError(t, err)
	require.NotEqual(t, canonicalKey, changedPathKey)
	require.False(t, sameIdentity(canonical, changedPath))
}

func TestTrustStoreRejectsAttestationAndProvenanceDowngrades(t *testing.T) {
	store := testTrustStore(t)
	identity, _ := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/heads/main")
	initial := map[string]bool{"linux|x86_64|gnu||tar.gz": true, "darwin|aarch64|||tgz": false}
	require.NoError(t, acceptTestRelease(t, store, identity, initial, "vendor"))
	require.ErrorContains(t, acceptTestRelease(t, store, identity, initial, "repackager"), "vendor to repackager")
	require.ErrorContains(t, acceptTestRelease(t, store, identity, map[string]bool{"linux|x86_64|gnu||tar.gz": false}, "vendor"), "provenance disappeared")
}

func TestTrustStoreCanonicalizesPersistedProvenanceAliases(t *testing.T) {
	store := testTrustStore(t)
	identity, _ := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/heads/main")
	seedTestProvenance(t, store, identity, map[string]bool{
		"linux|x86_64|gnu||tar.gz": true,
		"darwin|aarch64|||tgz":     true,
	})
	require.NoError(t, acceptTestRelease(t, store, identity, map[string]bool{
		"linux|amd64|gnu||tar.gz": true,
		"macos|arm64|||tgz":       true,
	}, "vendor"))

	doc, err := store.read()
	require.NoError(t, err)
	require.Equal(t, map[string]bool{
		"linux|amd64|gnu||tar.gz": true,
		"macos|arm64|||tgz":       true,
	}, doc.Projects[testProject].Provenance)
}

func TestTrustStoreCanonicalProvenanceDowngradeStillFails(t *testing.T) {
	tests := []struct {
		name      string
		candidate map[string]bool
	}{
		{name: "false", candidate: map[string]bool{"linux|amd64|gnu||tar.gz": false}},
		{name: "missing", candidate: map[string]bool{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := testTrustStore(t)
			identity, _ := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/heads/main")
			seedTestProvenance(t, store, identity, map[string]bool{"linux|x86_64|gnu||tar.gz": true})
			require.ErrorContains(t, acceptTestRelease(t, store, identity, test.candidate, "vendor"), "provenance disappeared")
		})
	}
}

func TestTrustStoreProvenanceAliasCollisionPreservesTrue(t *testing.T) {
	store := testTrustStore(t)
	identity, _ := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/heads/main")
	seedTestProvenance(t, store, identity, map[string]bool{
		"linux|x86_64|gnu||tar.gz": true,
		"linux|amd64|gnu||tar.gz":  false,
	})
	require.NoError(t, acceptTestRelease(t, store, identity, map[string]bool{
		"linux|amd64|gnu||tar.gz": true,
	}, "vendor"))

	doc, err := store.read()
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"linux|amd64|gnu||tar.gz": true}, doc.Projects[testProject].Provenance)
}

func TestProvenanceSelectorKeyNormalizationPreservesLibcWildcardAndUnknownKeys(t *testing.T) {
	require.Equal(t, "linux|amd64|||tar.gz", canonicalProvenanceSelectorKey("linux|x86_64|||tar.gz"),
		"empty libc remains empty rather than becoming GNU")
	require.Equal(t, "custom|mips64|unknown||tar.gz", canonicalProvenanceSelectorKey("custom|mips64|unknown||tar.gz"))
	require.Equal(t, "malformed|key", canonicalProvenanceSelectorKey("malformed|key"))
}

func TestTrustStorePersistsListSequencesAndRejectsRollbackOrSameSequenceReplacement(t *testing.T) {
	store := testTrustStore(t)
	identity, identityKey := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/heads/main")
	require.NoError(t, acceptTestList(t, store, identity, identityKey, 8, "sha256:first"))
	observed, err := store.observeList(testProject)
	require.NoError(t, err)
	require.True(t, observed.Accepted)
	require.NoError(t, acceptTestList(t, store, identity, identityKey, 9, "sha256:second"))
	require.ErrorContains(t, acceptTestList(t, store, identity, identityKey, 8, "sha256:first"), "sequence rollback")
	require.ErrorContains(t, acceptTestList(t, store, identity, identityKey, 9, "sha256:changed"), "without increasing sequence")
}

func TestTrustStoreRejectsStaleNoListResolutionAfterNewerYankIsAccepted(t *testing.T) {
	store := testTrustStore(t)
	oldObservation, err := store.observeList(testProject)
	require.NoError(t, err)
	require.False(t, oldObservation.Accepted)

	identity, identityKey := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/heads/main")
	// This is the newer signed list accepted by a concurrent resolution that
	// discovered the requested release had been yanked.
	require.NoError(t, store.acceptResolution(context.Background(), testProject, oldObservation, &listAcceptance{
		Signer: identity, IdentityKey: identityKey, Sequence: 12, Hash: "sha256:newer-yanked-list",
	}, nil))

	// The delayed caller observed no list and resolved an older endpoint release.
	// Its final transaction must compare against current state and reject it.
	err = store.acceptResolution(context.Background(), testProject, oldObservation, nil, &releaseAcceptance{
		Signer: identity, Attested: "vendor", Provenance: map[string]bool{"linux|x86_64|gnu||tar.gz": true},
	})
	require.ErrorContains(t, err, "trust state changed during resolution")

	current, err := store.observeList(testProject)
	require.NoError(t, err)
	require.Equal(t, uint64(12), current.Sequence)
	doc, err := store.read()
	require.NoError(t, err)
	require.Nil(t, doc.Projects[testProject].ReleaseSigner, "stale resolution must not accept release trust")
}

func TestTrustStoreCommitsListAndReleaseAcceptanceAtomically(t *testing.T) {
	store := testTrustStore(t)
	firstIdentity, _ := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/heads/main")
	require.NoError(t, acceptTestRelease(t, store, firstIdentity, map[string]bool{"linux|x86_64|gnu||tar.gz": true}, "vendor"))

	observed, err := store.observeList(testProject)
	require.NoError(t, err)
	changedIdentity, changedKey := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/publish.yml@refs/heads/main")
	err = store.acceptResolution(context.Background(), testProject, observed, &listAcceptance{
		Signer: changedIdentity, IdentityKey: changedKey, Sequence: 5, Hash: "sha256:list",
	}, &releaseAcceptance{
		Signer: changedIdentity, Attested: "vendor", Provenance: map[string]bool{"linux|x86_64|gnu||tar.gz": true},
	})
	require.ErrorContains(t, err, "differs from release signer")

	current, err := store.observeList(testProject)
	require.NoError(t, err)
	require.False(t, current.Accepted, "the list half must not be written if release trust fails")
	doc, err := store.read()
	require.NoError(t, err)
	require.Equal(t, firstIdentity.Workflow, doc.Projects[testProject].ReleaseSigner.Workflow)
}

func TestTrustStoreConcurrentProjectUpdatesDoNotLoseState(t *testing.T) {
	store := testTrustStore(t)
	identity, identityKey := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/heads/main")
	const projects = 16
	var wait sync.WaitGroup
	errors := make(chan error, projects)
	for i := 0; i < projects; i++ {
		project := "github.com/acme/driver" + strings.Repeat("x", i+1)
		wait.Add(1)
		go func() {
			defer wait.Done()
			observed, err := store.observeList(project)
			if err == nil {
				err = store.acceptResolution(context.Background(), project, observed, &listAcceptance{
					Signer: identity, IdentityKey: identityKey, Sequence: 1, Hash: "sha256:" + project,
				}, nil)
			}
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	for i := 0; i < projects; i++ {
		project := "github.com/acme/driver" + strings.Repeat("x", i+1)
		observed, err := store.observeList(project)
		require.NoError(t, err)
		require.True(t, observed.Accepted, "state for %s was lost", project)
	}
}

func TestTrustStoreCorruptionFailsClosedWithoutFirstUseFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust", "packslip.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	corrupt := []byte(`{"version":1,"projects":{`)
	require.NoError(t, os.WriteFile(path, corrupt, 0o600))
	store, err := NewTrustStoreAt(path)
	require.NoError(t, err)
	observed, err := store.observeList(testProject)
	require.ErrorContains(t, err, "decode packslip trust store")
	require.False(t, observed.Accepted)
	identity, identityKey := testTrustedIdentity(t, "https://github.com/acme/driver/.github/workflows/release.yml@refs/heads/main")
	require.ErrorContains(t, acceptTestList(t, store, identity, identityKey, 1, "sha256:first"), "decode packslip trust store")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, corrupt, after)
}

func TestTrustStoreKeepsStrictUnknownFieldRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust", "packslip.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	data := []byte(`{"version":1,"projects":{},"future_field":true}`)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	store, err := NewTrustStoreAt(path)
	require.NoError(t, err)
	_, err = store.observeList(testProject)
	require.ErrorContains(t, err, "unknown field")
}
