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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/columnar-tech/dbc/internal"
	"github.com/columnar-tech/dbc/internal/atomicfile"
	"github.com/columnar-tech/dbc/internal/fslock"
)

const trustStoreVersion = 1

type trustDocument struct {
	Version  int                      `json:"version"`
	Projects map[string]*projectTrust `json:"projects"`
}

type projectTrust struct {
	ReleaseSigner *trustedIdentity     `json:"release_signer,omitempty"`
	ListSigner    *trustedIdentity     `json:"list_signer,omitempty"`
	AttestedBy    string               `json:"attested_by,omitempty"`
	Provenance    map[string]bool      `json:"provenance,omitempty"`
	Lists         map[string]listTrust `json:"lists,omitempty"`
}

type trustedIdentity struct {
	Scheme   string `json:"scheme"`
	Issuer   string `json:"issuer"`
	Workflow string `json:"workflow"`
}

type listTrust struct {
	Accepted bool   `json:"accepted"`
	Sequence uint64 `json:"sequence"`
	Hash     string `json:"hash"`
}

type listObservation struct {
	Accepted    bool
	IdentityKey string
	Sequence    uint64
	Hash        string
}

type listAcceptance struct {
	Signer      trustedIdentity
	IdentityKey string
	Sequence    uint64
	Hash        string
}

type releaseAcceptance struct {
	Signer     trustedIdentity
	Attested   string
	Provenance map[string]bool
}

// NewTrustStore returns the dbc-local persistent packslip trust store at
// $XDG_CONFIG_HOME/columnar/dbc/trust/packslip.json (or the platform's user
// config equivalent).
func NewTrustStore() (*TrustStore, error) {
	configPath, err := internal.GetUserConfigPath()
	if err != nil {
		return nil, fmt.Errorf("find dbc user config directory for packslip trust: %w", err)
	}
	return NewTrustStoreAt(filepath.Join(configPath, "trust", "packslip.json"))
}

// NewTrustStoreAt constructs a persistent trust store at path. The file is
// created on first accepted metadata update, not during construction.
func NewTrustStoreAt(path string) (*TrustStore, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("packslip trust-store path must be absolute: %q", path)
	}
	return &TrustStore{path: filepath.Clean(path)}, nil
}

func (s *TrustStore) observeList(project string) (listObservation, error) {
	doc, err := s.read()
	if err != nil {
		return listObservation{}, err
	}
	state := doc.Projects[project]
	if state == nil {
		return listObservation{}, nil
	}
	var observed listObservation
	for identityKey, list := range state.Lists {
		if list.Accepted {
			if observed.Accepted {
				return listObservation{}, fmt.Errorf("packslip trust store has multiple accepted release lists for %s", project)
			}
			observed = listObservation{Accepted: true, IdentityKey: identityKey, Sequence: list.Sequence, Hash: list.Hash}
		}
	}
	return observed, nil
}

// acceptResolution compares the trust state observed before network work with
// the latest state under one lock, then commits the verified list and release
// together. A stale resolution cannot overwrite newer yanks or sequences.
func (s *TrustStore) acceptResolution(ctx context.Context, project string, observed listObservation, list *listAcceptance, release *releaseAcceptance) error {
	return s.update(ctx, project, func(state *projectTrust) error {
		current := acceptedListObservation(state)
		if current != observed {
			return fmt.Errorf("packslip trust state changed during resolution for %s; retry resolution", project)
		}
		if list != nil {
			if err := applyListAcceptance(state, project, *list); err != nil {
				return err
			}
		}
		if release != nil {
			if err := applyReleaseAcceptance(state, project, *release); err != nil {
				return err
			}
		}
		return nil
	})
}

func acceptedListObservation(state *projectTrust) listObservation {
	if state == nil {
		return listObservation{}
	}
	for identityKey, list := range state.Lists {
		if list.Accepted {
			return listObservation{Accepted: true, IdentityKey: identityKey, Sequence: list.Sequence, Hash: list.Hash}
		}
	}
	return listObservation{}
}

func applyListAcceptance(state *projectTrust, project string, candidate listAcceptance) error {
	if state.Lists == nil {
		state.Lists = make(map[string]listTrust)
	}
	for previousIdentity, previous := range state.Lists {
		if previous.Accepted && previousIdentity != candidate.IdentityKey {
			return fmt.Errorf("signed release-list identity changed for %s", project)
		}
	}
	previous := state.Lists[candidate.IdentityKey]
	if previous.Accepted {
		if candidate.Sequence < previous.Sequence {
			return fmt.Errorf("signed release-list sequence rollback for %s: got %d after %d", project, candidate.Sequence, previous.Sequence)
		}
		if candidate.Sequence == previous.Sequence && candidate.Hash != previous.Hash {
			return fmt.Errorf("signed release list changed without increasing sequence for %s", project)
		}
	}
	if state.ListSigner != nil && !sameIdentity(*state.ListSigner, candidate.Signer) {
		return fmt.Errorf("signed release-list signer changed for %s", project)
	}
	if state.ReleaseSigner != nil && !sameIdentity(*state.ReleaseSigner, candidate.Signer) {
		return fmt.Errorf("signed release-list signer differs from release signer for %s", project)
	}
	copy := candidate.Signer
	state.ListSigner = &copy
	state.Lists[candidate.IdentityKey] = listTrust{Accepted: true, Sequence: candidate.Sequence, Hash: candidate.Hash}
	return nil
}

func applyReleaseAcceptance(state *projectTrust, project string, candidate releaseAcceptance) error {
	if state.ReleaseSigner != nil && !sameIdentity(*state.ReleaseSigner, candidate.Signer) {
		return fmt.Errorf("release signer changed for %s", project)
	}
	if state.ListSigner != nil && !sameIdentity(*state.ListSigner, candidate.Signer) {
		return fmt.Errorf("release signer differs from signed release-list signer for %s", project)
	}
	if state.AttestedBy == "vendor" && candidate.Attested == "repackager" {
		return fmt.Errorf("release attestation changed from vendor to repackager for %s", project)
	}
	previousProvenance := canonicalizeProvenanceSelectors(state.Provenance)
	candidateProvenance := canonicalizeProvenanceSelectors(candidate.Provenance)
	for selector, wasPresent := range previousProvenance {
		if wasPresent && !candidateProvenance[selector] {
			return fmt.Errorf("release provenance disappeared for artifact selector %s", selector)
		}
	}
	copy := candidate.Signer
	state.ReleaseSigner = &copy
	if state.AttestedBy == "" || candidate.Attested == "vendor" {
		state.AttestedBy = candidate.Attested
	}
	mergedProvenance := previousProvenance
	if mergedProvenance == nil && candidateProvenance != nil {
		mergedProvenance = make(map[string]bool, len(candidateProvenance))
	}
	for selector, present := range candidateProvenance {
		if present || !mergedProvenance[selector] {
			mergedProvenance[selector] = present
		}
	}
	state.Provenance = mergedProvenance
	return nil
}

// canonicalProvenanceSelectorKey normalizes only established OS and
// architecture aliases in the five-field Packslip selector key. A malformed
// key or an unknown token is preserved byte-for-byte so existing trust is not
// accidentally broadened. In particular, an empty libc remains a wildcard.
func canonicalProvenanceSelectorKey(selector string) string {
	parts := strings.Split(selector, "|")
	if len(parts) != 5 {
		return selector
	}
	for _, part := range parts {
		if part != "" && !validToken(part) {
			return selector
		}
	}
	switch parts[0] {
	case "darwin":
		parts[0] = "macos"
	}
	switch parts[1] {
	case "x86_64":
		parts[1] = "amd64"
	case "aarch64":
		parts[1] = "arm64"
	}
	return strings.Join(parts, "|")
}

func canonicalizeProvenanceSelectors(provenance map[string]bool) map[string]bool {
	if provenance == nil {
		return nil
	}
	canonical := make(map[string]bool, len(provenance))
	for selector, present := range provenance {
		key := canonicalProvenanceSelectorKey(selector)
		if present || !canonical[key] {
			canonical[key] = present
		}
	}
	return canonical
}

func (s *TrustStore) update(ctx context.Context, project string, mutate func(*projectTrust) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create packslip trust-store directory: %w", err)
	}
	lock, err := fslock.AcquireContext(ctx, filepath.Join(dir, "packslip.lock"))
	if err != nil {
		return fmt.Errorf("acquire packslip trust-store lock: %w", err)
	}
	writeErr := func() error {
		doc, err := s.read()
		if err != nil {
			return err
		}
		state := doc.Projects[project]
		if state == nil {
			state = &projectTrust{}
			doc.Projects[project] = state
		}
		if err := mutate(state); err != nil {
			return err
		}
		return s.write(doc)
	}()
	releaseErr := lock.Release()
	if writeErr != nil && releaseErr != nil {
		return errors.Join(writeErr, fmt.Errorf("release packslip trust-store lock: %w", releaseErr))
	}
	if writeErr != nil {
		return writeErr
	}
	if releaseErr != nil {
		return fmt.Errorf("release packslip trust-store lock: %w", releaseErr)
	}
	return nil
}

func (s *TrustStore) read() (trustDocument, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return trustDocument{Version: trustStoreVersion, Projects: make(map[string]*projectTrust)}, nil
	}
	if err != nil {
		return trustDocument{}, fmt.Errorf("read packslip trust store: %w", err)
	}
	var doc trustDocument
	if err := decodeStrict(data, &doc); err != nil {
		return trustDocument{}, fmt.Errorf("decode packslip trust store: %w", err)
	}
	if doc.Version != trustStoreVersion || doc.Projects == nil {
		return trustDocument{}, fmt.Errorf("unsupported or incomplete packslip trust-store format")
	}
	for project, state := range doc.Projects {
		canonical, err := normalizeProject(project)
		if err != nil || canonical != project || state == nil {
			return trustDocument{}, fmt.Errorf("invalid project entry in packslip trust store: %q", project)
		}
		acceptedCount := 0
		for identityKey, list := range state.Lists {
			if strings.TrimSpace(identityKey) == "" || (list.Accepted && list.Hash == "") {
				return trustDocument{}, fmt.Errorf("invalid signed release-list state for %s", project)
			}
			if list.Accepted {
				acceptedCount++
			}
		}
		if acceptedCount > 1 || state.ListSigner != nil && acceptedCount == 0 {
			return trustDocument{}, fmt.Errorf("inconsistent accepted release-list state for %s", project)
		}
	}
	return doc, nil
}

func (s *TrustStore) write(doc trustDocument) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode packslip trust store: %w", err)
	}
	if err := atomicfile.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write packslip trust store: %w", err)
	}
	return nil
}

func sameIdentity(left, right trustedIdentity) bool {
	return left.Scheme == right.Scheme && left.Issuer == right.Issuer && left.Workflow == right.Workflow
}

func identityTrust(scheme, issuer, signer string) (trustedIdentity, string, error) {
	if scheme != "sigstore-oidc" || issuer != GitHubOIDCIssuer {
		return trustedIdentity{}, "", fmt.Errorf("unsupported GitHub packslip signer identity")
	}
	workflow, err := workflowContinuityKey(signer)
	if err != nil {
		return trustedIdentity{}, "", err
	}
	identity := trustedIdentity{Scheme: scheme, Issuer: issuer, Workflow: workflow}
	return identity, scheme + "|" + issuer + "|" + workflow, nil
}

func workflowContinuityKey(signer string) (string, error) {
	separator := strings.LastIndexByte(signer, '@')
	if separator < 0 || separator == len(signer)-1 {
		return "", fmt.Errorf("GitHub signer identity %q is not a GitHub workflow identity", signer)
	}
	identity := signer[:separator]
	colon := strings.Index(identity, "://")
	if colon < 0 || !strings.EqualFold(identity[:colon], "https") {
		return "", fmt.Errorf("GitHub signer identity %q is not a GitHub workflow identity", signer)
	}
	path := identity[colon+3:]
	segments := strings.SplitN(path, "/", 2)
	if len(segments) != 2 || !strings.EqualFold(segments[0], "github.com") {
		return "", fmt.Errorf("GitHub signer identity %q is not a GitHub workflow identity", signer)
	}
	parts := strings.Split(segments[1], "/")
	if len(parts) < 5 || parts[0] == "" || parts[1] == "" || parts[2] != ".github" || parts[3] != "workflows" {
		return "", fmt.Errorf("GitHub signer identity %q is not a workflow path", signer)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("GitHub signer identity %q has an invalid path", signer)
		}
	}
	return "https://github.com/" + strings.ToLower(parts[0]) + "/" + strings.ToLower(parts[1]) + "/" + strings.Join(parts[2:], "/"), nil
}
