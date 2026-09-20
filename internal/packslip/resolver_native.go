// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build !js

package packslip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/columnar-tech/dbc/internal/packslipverify"
	"github.com/columnar-tech/dbc/internal/resolution"
)

type nativeResolver struct {
	discovery *GitHubPackslipDiscovery
	verifier  packslipverify.PackslipVerifier
	trust     *TrustStore
	now       func() time.Time
}

type verifiedRelease struct {
	statement   *parsedRelease
	identity    trustedIdentity
	identityKey string
	bundleHash  string
}

// NewResolver constructs the native GitHub packslip resolver.
func NewResolver(config Config) (Resolver, error) {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	discovery, err := NewGitHubPackslipDiscovery(client, config.GitHubAPIBaseURL, config.GitHubRawBaseURL)
	if err != nil {
		return nil, err
	}
	verifier := config.Verifier
	if verifier == nil {
		verifier, err = packslipverify.New(packslipverify.Config{
			HTTPClient:      client,
			TrustedRootJSON: config.TrustedRootJSON,
		})
		if err != nil {
			return nil, fmt.Errorf("create packslip Sigstore verifier: %w", err)
		}
	}
	trust := config.TrustStore
	if trust == nil {
		trust, err = NewTrustStore()
		if err != nil {
			return nil, err
		}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &nativeResolver{discovery: discovery, verifier: verifier, trust: trust, now: now}, nil
}

func (r *nativeResolver) Resolve(ctx context.Context, source PackslipSource, request Request) (resolution.ResolvedRelease, error) {
	if err := ctx.Err(); err != nil {
		return resolution.ResolvedRelease{}, err
	}
	project, err := normalizeProject(source.Project)
	if err != nil {
		return resolution.ResolvedRelease{}, err
	}
	if strings.TrimSpace(request.DriverID) == "" {
		return resolution.ResolvedRelease{}, errors.New("packslip resolution requires a driver ID")
	}
	if !semverPattern.MatchString(request.Version) {
		return resolution.ResolvedRelease{}, fmt.Errorf("requested packslip version %q must be an exact SemVer 2.0.0 version", request.Version)
	}
	source.Project = project
	identityPolicy, err := githubIdentityPolicy(project)
	if err != nil {
		return resolution.ResolvedRelease{}, err
	}
	observedList, err := r.trust.observeList(project)
	if err != nil {
		return resolution.ResolvedRelease{}, err
	}

	var signedList *parsedList
	var acceptedList *listAcceptance
	var listURL, listHash string
	listURL, err = r.discovery.ReleaseListURL(source)
	if err != nil {
		return resolution.ResolvedRelease{}, err
	}
	listBytes, _, listErr := r.discovery.get(ctx, listURL, false)
	switch {
	case errors.Is(listErr, errHTTPNotFound):
		if observedList.Accepted {
			return resolution.ResolvedRelease{}, fmt.Errorf("previously accepted signed release list for %s is missing", project)
		}
		listURL = ""
	case listErr != nil:
		return resolution.ResolvedRelease{}, fmt.Errorf("fetch supplementary signed release list for %s: %w", project, listErr)
	default:
		verified, err := r.verifyBundle(ctx, listBytes, identityPolicy)
		if err != nil {
			return resolution.ResolvedRelease{}, fmt.Errorf("verify signed release list: %w", err)
		}
		list, err := parseList(verified.payload, project)
		if err != nil {
			return resolution.ResolvedRelease{}, err
		}
		if err := checkDeclaredIdentity(list.predicate.Identity, verified.verified); err != nil {
			return resolution.ResolvedRelease{}, err
		}
		expires, _ := parseUTC("expires_at", list.predicate.Expires)
		if !r.now().Before(expires) {
			return resolution.ResolvedRelease{}, fmt.Errorf("signed release list for %s expired at %s", project, list.predicate.Expires)
		}
		identity, identityKey, err := identityTrust(list.predicate.Identity.Scheme, *list.predicate.Identity.Issuer, verified.verified.Signer)
		if err != nil {
			return resolution.ResolvedRelease{}, err
		}
		listHash = digestBytes(listBytes)
		acceptedList = &listAcceptance{Signer: identity, IdentityKey: identityKey, Sequence: *list.predicate.Sequence, Hash: listHash}
		signedList = list
	}

	var listed *releaseRef
	if signedList != nil {
		for i := range signedList.predicate.Releases {
			if signedList.predicate.Releases[i].Version == request.Version {
				listed = &signedList.predicate.Releases[i]
				break
			}
		}
		if listed != nil && listed.Status == "yanked" {
			if err := r.trust.acceptResolution(ctx, project, observedList, acceptedList, nil); err != nil {
				return resolution.ResolvedRelease{}, err
			}
			return resolution.ResolvedRelease{}, fmt.Errorf("%w: %s", ErrYankedRelease, request.Version)
		}
	}

	githubReleases, err := r.discovery.ListReleases(ctx, source)
	if err != nil {
		return resolution.ResolvedRelease{}, err
	}
	releaseByTag := make(map[string]githubRelease, len(githubReleases))
	var endpointMatches []githubRelease
	for _, release := range githubReleases {
		releaseByTag[release.TagName] = release
		version, ok := tagVersion(release.TagName, project)
		if ok && version == request.Version {
			endpointMatches = append(endpointMatches, release)
		}
	}
	var bundleURL string
	var bundleBytes []byte
	var releaseAssets []githubAsset
	var expectedTag string
	if listed != nil {
		bundleURL = listed.Packslip
		if release, ok := releaseByTag[stringValue(listed.Tag)]; stringValue(listed.Tag) != "" && ok {
			releaseAssets = release.Assets
			expectedTag = release.TagName
		} else {
			expectedTag = stringValue(listed.Tag)
			if expectedTag == "" && len(endpointMatches) == 1 {
				releaseAssets = endpointMatches[0].Assets
			}
		}
		bundleBytes, _, err = r.discovery.get(ctx, bundleURL, false)
		if err != nil {
			return resolution.ResolvedRelease{}, fmt.Errorf("fetch signed packslip for %s: %w", request.Version, err)
		}
		pinned := signedList.byURL[listed.Packslip].Digest["sha256"]
		if digestHex(bundleBytes) != pinned {
			return resolution.ResolvedRelease{}, fmt.Errorf("signed release list digest mismatch for %s", request.Version)
		}
	} else {
		if len(endpointMatches) > 1 {
			return resolution.ResolvedRelease{}, fmt.Errorf("multiple GitHub tags map to packslip version %s", request.Version)
		}
		if len(endpointMatches) == 0 {
			if acceptedList != nil {
				if err := r.trust.acceptResolution(ctx, project, observedList, acceptedList, nil); err != nil {
					return resolution.ResolvedRelease{}, err
				}
			}
			return resolution.ResolvedRelease{}, fmt.Errorf("%w: %s for %s", ErrReleaseNotFound, request.Version, project)
		}
		release := endpointMatches[0]
		expectedTag = release.TagName
		releaseAssets = release.Assets
		var candidates []githubAsset
		for _, asset := range release.Assets {
			if strings.HasPrefix(asset.Name, "packslip") && strings.HasSuffix(asset.Name, ".sigstore.json") {
				candidates = append(candidates, asset)
			}
		}
		if len(candidates) == 0 {
			return resolution.ResolvedRelease{}, fmt.Errorf("GitHub release %q has no packslip bundle asset", release.TagName)
		}
		var found *verifiedRelease
		for _, candidate := range candidates {
			if err := validateHTTPSURL(candidate.BrowserDownloadURL); err != nil {
				return resolution.ResolvedRelease{}, fmt.Errorf("invalid GitHub packslip asset URL: %w", err)
			}
			bytes, _, err := r.discovery.get(ctx, candidate.BrowserDownloadURL, false)
			if err != nil {
				return resolution.ResolvedRelease{}, fmt.Errorf("fetch GitHub packslip asset %q: %w", candidate.Name, err)
			}
			verified, err := r.verifyReleaseBundle(ctx, bytes, identityPolicy, "", "")
			if err != nil {
				return resolution.ResolvedRelease{}, fmt.Errorf("verify GitHub packslip asset %q: %w", candidate.Name, err)
			}
			if verified.statement.predicate.Project != project {
				continue
			}
			if verified.statement.predicate.Version != request.Version {
				return resolution.ResolvedRelease{}, fmt.Errorf("GitHub release tag %q maps to %s but signed packslip says %s", release.TagName, request.Version, verified.statement.predicate.Version)
			}
			if _, ok := tagVersion(release.TagName, project); !ok {
				return resolution.ResolvedRelease{}, fmt.Errorf("GitHub release tag %q does not name a SemVer version", release.TagName)
			}
			if tagVersionValue, _ := tagVersion(release.TagName, project); tagVersionValue != verified.statement.predicate.Version {
				return resolution.ResolvedRelease{}, fmt.Errorf("GitHub release tag %q does not match signed packslip version %s", release.TagName, verified.statement.predicate.Version)
			}
			if sourceTag := verified.statement.predicate.Source; sourceTag != nil && sourceTag.Tag != nil && *sourceTag.Tag != release.TagName {
				return resolution.ResolvedRelease{}, fmt.Errorf("signed packslip source.tag %q does not match GitHub tag %q", *sourceTag.Tag, release.TagName)
			}
			if found != nil {
				return resolution.ResolvedRelease{}, fmt.Errorf("GitHub release has multiple signed packslips for %s", project)
			}
			verified.bundleHash = digestBytes(bytes)
			verified.statement.predicate.Project = project
			bundleURL, bundleBytes = candidate.BrowserDownloadURL, bytes
			found = verified
		}
		if found == nil {
			return resolution.ResolvedRelease{}, fmt.Errorf("GitHub release %q has no signed packslip for %s", release.TagName, project)
		}
		return r.finishResolved(ctx, project, request, observedList, acceptedList, found, bundleURL, bundleBytes, listURL, listHash, releaseAssets)
	}

	verified, err := r.verifyReleaseBundle(ctx, bundleBytes, identityPolicy, project, request.Version)
	if err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("verify signed packslip for %s: %w", request.Version, err)
	}
	if listed.PublishedAt != "" {
		listedAt, _ := parseUTC("release-list published_at", listed.PublishedAt)
		publishedAt, _ := parseUTC("published_at", verified.statement.predicate.PublishedAt)
		if !listedAt.Equal(publishedAt) {
			return resolution.ResolvedRelease{}, errors.New("release-list publication time does not match signed packslip")
		}
	}
	if expectedTag != "" && listed.Tag != nil {
		if source := verified.statement.predicate.Source; source == nil || source.Tag == nil || *source.Tag != expectedTag {
			return resolution.ResolvedRelease{}, fmt.Errorf("signed packslip source.tag does not match release-list tag %q", expectedTag)
		}
	}
	verified.bundleHash = digestBytes(bundleBytes)
	return r.finishResolved(ctx, project, request, observedList, acceptedList, verified, bundleURL, bundleBytes, listURL, listHash, releaseAssets)
}

type verifierResult struct {
	verified *packslipverify.VerifiedBundle
	payload  []byte
}

func (r *nativeResolver) verifyBundle(ctx context.Context, bundleBytes []byte, policy packslipverify.IdentityPolicy) (*verifierResult, error) {
	algorithm, digest, peekedPayload, err := peekBundleDigest(bundleBytes)
	if err != nil {
		return nil, err
	}
	verified, err := r.verifier.Verify(ctx, bundleBytes, policy, packslipverify.ArtifactDigest{Algorithm: algorithm, Hex: digest})
	if err != nil {
		return nil, err
	}
	if verified == nil {
		return nil, errors.New("Sigstore verifier returned no verified bundle")
	}
	if !bytes.Equal(peekedPayload, verified.SignedPayload) {
		return nil, errors.New("Sigstore verifier returned a payload different from the bundle payload")
	}
	if verified.Issuer != GitHubOIDCIssuer || !policyMatchesSigner(policy, verified.Signer) {
		return nil, errors.New("verified Sigstore identity does not match the requested GitHub project")
	}
	return &verifierResult{verified: verified, payload: verified.SignedPayload}, nil
}

func (r *nativeResolver) verifyReleaseBundle(ctx context.Context, bundleBytes []byte, policy packslipverify.IdentityPolicy, expectedProject, expectedVersion string) (*verifiedRelease, error) {
	result, err := r.verifyBundle(ctx, bundleBytes, policy)
	if err != nil {
		return nil, err
	}
	release, err := parseRelease(result.payload, expectedProject)
	if err != nil {
		return nil, err
	}
	if expectedVersion != "" && release.predicate.Version != expectedVersion {
		return nil, fmt.Errorf("signed packslip version %q does not match requested version %q", release.predicate.Version, expectedVersion)
	}
	if err := checkDeclaredIdentity(release.predicate.Identity, result.verified); err != nil {
		return nil, err
	}
	identity, identityKey, err := identityTrust(release.predicate.Identity.Scheme, *release.predicate.Identity.Issuer, result.verified.Signer)
	if err != nil {
		return nil, err
	}
	return &verifiedRelease{statement: release, identity: identity, identityKey: identityKey}, nil
}

func (r *nativeResolver) finishResolved(ctx context.Context, project string, request Request, observedList listObservation, acceptedList *listAcceptance, verified *verifiedRelease, bundleURL string, bundleBytes []byte, listURL, listHash string, assets []githubAsset) (resolution.ResolvedRelease, error) {
	if (listURL == "") != (listHash == "") {
		return resolution.ResolvedRelease{}, errors.New("release-index evidence requires both a URL and hash")
	}
	if err := validateSupportedArtifactSet(verified.statement); err != nil {
		return resolution.ResolvedRelease{}, err
	}
	// TODO(packslip): Before exposing Packslip as a public dbc source, require a
	// signed dbc consumer declaration in Packslip predicate/artifact extensions.
	// The prototype currently treats supported tar.gz/tgz artifacts as dbc
	// package candidates. The declaration should identify the dbc ADBC driver
	// and package contract, runtime identity, installable artifacts, and package
	// format; release/artifact field ownership remains undecided. After download,
	// verify it against dbc-package.toml and fail on mismatches. Keep the schema
	// unspecified until that contract is designed.
	targets, err := deriveConcreteTargets(verified.statement)
	if err != nil {
		return resolution.ResolvedRelease{}, err
	}
	artifacts := make([]resolution.Artifact, 0, len(targets))
	for _, target := range targets {
		selected, err := selectArtifact(verified.statement, target)
		if err != nil {
			return resolution.ResolvedRelease{}, fmt.Errorf("select packslip artifact for %s: %w", describeTarget(target), err)
		}
		artifact, err := convertSelectedArtifact(verified.statement, selected, assets, target)
		if err != nil {
			return resolution.ResolvedRelease{}, fmt.Errorf("resolve packslip artifact for %s: %w", describeTarget(target), err)
		}
		artifacts = append(artifacts, artifact)
	}
	provenance, err := provenancePresence(verified.statement)
	if err != nil {
		return resolution.ResolvedRelease{}, err
	}
	checksum := digestBytes(bundleBytes)
	evidence := []resolution.Evidence{{
		Kind:     resolution.EvidenceKindReleaseMetadata,
		Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: bundleURL},
		Hash:     checksum,
	}}
	if listURL != "" && listHash != "" {
		evidence = append(evidence, resolution.Evidence{
			Kind:     resolution.EvidenceKindReleaseIndex,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: listURL},
			Hash:     listHash,
		})
	}
	result := resolution.ResolvedRelease{
		DriverID:  request.DriverID,
		Version:   verified.statement.predicate.Version,
		Source:    resolution.SourceSpec{Type: "packslip", Reference: project},
		Evidence:  evidence,
		Artifacts: artifacts,
	}
	if err := resolution.ValidateResolvedRelease(result); err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("invalid resolved packslip release: %w", err)
	}
	releaseTrust := &releaseAcceptance{Signer: verified.identity, Attested: verified.statement.attested, Provenance: provenance}
	if err := r.trust.acceptResolution(ctx, project, observedList, acceptedList, releaseTrust); err != nil {
		return resolution.ResolvedRelease{}, err
	}
	return result, nil
}

func describeTarget(target Target) string {
	target = resolution.CanonicalTarget(target)
	return fmt.Sprintf("os=%q arch=%q libc=%q variant=%q", target.OS, target.Arch, target.LibC, target.Variant)
}

// deriveConcreteTargets returns the finite set of concrete targets represented
// by supported artifacts with explicit OS and architecture selectors. A
// wildcard-only artifact cannot describe an enumerable platform set, so it
// does not create a target by itself.
func deriveConcreteTargets(release *parsedRelease) ([]Target, error) {
	targetSet := make(map[Target]struct{}, len(release.predicate.Artifacts))
	for i := range release.predicate.Artifacts {
		artifact := &release.predicate.Artifacts[i]
		if !supportedArchiveFormat(stringValue(artifact.Format)) || artifact.OS == nil || artifact.Arch == nil {
			continue
		}
		target := resolution.CanonicalTarget(Target{
			OS:      *artifact.OS,
			Arch:    *artifact.Arch,
			LibC:    stringValue(artifact.LibC),
			Variant: stringValue(artifact.Variant),
		})
		if err := resolution.ValidateConcreteTarget(target); err != nil {
			return nil, fmt.Errorf("packslip artifact %q does not identify a concrete target: %w", artifact.Name, err)
		}
		targetSet[target] = struct{}{}
	}
	if len(targetSet) == 0 {
		return nil, errors.New("packslip release has no supported artifact with explicit OS and architecture selectors")
	}
	targets := make([]Target, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		left, right := targets[i], targets[j]
		if left.OS != right.OS {
			return left.OS < right.OS
		}
		if left.Arch != right.Arch {
			return left.Arch < right.Arch
		}
		if left.LibC != right.LibC {
			return left.LibC < right.LibC
		}
		return left.Variant < right.Variant
	})
	return targets, nil
}

func checkDeclaredIdentity(declared releaseIdentity, verified *packslipverify.VerifiedBundle) error {
	if declared.Scheme != "sigstore-oidc" || declared.Issuer == nil || *declared.Issuer != verified.Issuer || declared.KeyID != verified.Signer {
		return errors.New("signed packslip identity declaration does not match its verified Sigstore identity")
	}
	return nil
}

func githubIdentityPolicy(project string) (packslipverify.IdentityPolicy, error) {
	project, err := normalizeProject(project)
	if err != nil {
		return packslipverify.IdentityPolicy{}, err
	}
	owner, repo, _ := projectParts(project)
	pattern := `^https://(?i:github\.com/` + regexp.QuoteMeta(owner) + `/` + regexp.QuoteMeta(repo) + `)/\.github/workflows/[^@]+@[^@]+$`
	return packslipverify.IdentityPolicy{Issuer: GitHubOIDCIssuer, SubjectRegex: pattern}, nil
}

func policyMatchesSigner(policy packslipverify.IdentityPolicy, signer string) bool {
	if policy.Subject != "" {
		return signer == policy.Subject
	}
	compiled, err := regexp.Compile(policy.SubjectRegex)
	return err == nil && compiled.MatchString(signer)
}

func digestBytes(value []byte) string { return "sha256:" + digestHex(value) }

func digestHex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func provenancePresence(release *parsedRelease) (map[string]bool, error) {
	if err := validateSupportedArtifactSet(release); err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(release.predicate.Artifacts))
	for _, artifact := range release.predicate.Artifacts {
		if !supportedArchiveFormat(stringValue(artifact.Format)) {
			continue
		}
		selector := artifactSelectorKey(&artifact)
		if _, exists := result[selector]; exists {
			return nil, fmt.Errorf("packslip provenance selectors are not unique: %q", selector)
		}
		result[selector] = len(artifact.Provenance) > 0
	}
	return result, nil
}

var _ Resolver = (*nativeResolver)(nil)
