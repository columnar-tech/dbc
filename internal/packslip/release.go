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

package packslip

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/columnar-tech/dbc/internal/sourceidentity"
)

var (
	semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	tokenPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
)

// parseRelease checks the release/v1 wire shape and all digest relationships.
// The caller invokes it only on a payload returned by the verifier.
func parseRelease(payload []byte, expectedProject string) (*parsedRelease, error) {
	var envelope statementEnvelope
	if err := decodeWire(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode packslip statement: %w", err)
	}
	if envelope.Type != StatementType {
		return nil, fmt.Errorf("packslip _type %q is unsupported", envelope.Type)
	}
	if envelope.PredicateType != ReleasePredicateType {
		return nil, fmt.Errorf("packslip predicateType %q is not %s", envelope.PredicateType, ReleasePredicateType)
	}
	var predicate releasePredicate
	if err := decodeWire(envelope.Predicate, &predicate); err != nil {
		return nil, fmt.Errorf("decode packslip release predicate: %w", err)
	}
	projectKey, err := sourceidentity.Parse(sourceidentity.Packslip, predicate.Project)
	if err != nil {
		return nil, fmt.Errorf("packslip project %q is invalid: %w", predicate.Project, err)
	}
	canonicalProject := projectKey.Reference
	if expectedProject != "" {
		expectedKey, err := sourceidentity.Parse(sourceidentity.Packslip, expectedProject)
		if err != nil {
			return nil, err
		}
		expectedProject = expectedKey.Reference
		if canonicalProject != expectedProject {
			return nil, fmt.Errorf("packslip project %q does not match requested project %q", predicate.Project, expectedProject)
		}
	}
	predicate.Project = canonicalProject
	if !semverPattern.MatchString(predicate.Version) {
		return nil, fmt.Errorf("packslip version %q is not SemVer 2.0.0", predicate.Version)
	}
	if _, err := parseUTC("published_at", predicate.PublishedAt); err != nil {
		return nil, err
	}
	if predicate.Source != nil {
		if predicate.Source.Repo == "" {
			return nil, errors.New("packslip source.repo is empty")
		}
		if err := validateHTTPSURL(predicate.Source.Repo); err != nil {
			return nil, fmt.Errorf("packslip source.repo: %w", err)
		}
		if predicate.Source.Tag != nil && *predicate.Source.Tag == "" {
			return nil, errors.New("packslip source.tag is empty")
		}
	}
	if predicate.NotesURL != "" {
		if err := validateHTTPSURL(predicate.NotesURL); err != nil {
			return nil, fmt.Errorf("packslip notes_url: %w", err)
		}
	}
	if len(predicate.Artifacts) == 0 {
		return nil, errors.New("packslip release has no artifacts")
	}
	if err := validateIdentityShape(predicate.Identity); err != nil {
		return nil, err
	}
	attested := predicate.AttestedBy
	if attested == "" {
		attested = "vendor"
	}
	if attested != "vendor" && attested != "repackager" {
		return nil, fmt.Errorf("unsupported packslip attested_by value %q", attested)
	}

	byName := make(map[string]subject, len(envelope.Subject))
	for _, entry := range envelope.Subject {
		if entry.Name == "" {
			return nil, errors.New("packslip subject name is empty")
		}
		if _, exists := byName[entry.Name]; exists {
			return nil, fmt.Errorf("packslip has duplicate subject %q", entry.Name)
		}
		if err := validateSubjectDigest(entry); err != nil {
			return nil, err
		}
		byName[entry.Name] = entry
	}
	seenArtifacts := make(map[string]bool, len(predicate.Artifacts))
	seenSelectors := make(map[string]string, len(predicate.Artifacts))
	for i := range predicate.Artifacts {
		artifact := &predicate.Artifacts[i]
		if artifact.Name == "" || strings.ContainsAny(artifact.Name, "/\\\x00") || artifact.Name == "." || artifact.Name == ".." {
			return nil, fmt.Errorf("packslip artifact %q has an invalid name", artifact.Name)
		}
		if seenArtifacts[artifact.Name] {
			return nil, fmt.Errorf("packslip has duplicate artifact %q", artifact.Name)
		}
		seenArtifacts[artifact.Name] = true
		if _, ok := byName[artifact.Name]; !ok {
			return nil, fmt.Errorf("packslip artifact %q has no subject", artifact.Name)
		}
		if artifact.Size == nil {
			return nil, fmt.Errorf("packslip artifact %q has no size", artifact.Name)
		}
		if artifact.Format == nil || !validToken(*artifact.Format) {
			return nil, fmt.Errorf("packslip artifact %q has an invalid or missing format", artifact.Name)
		}
		canonicalizeArtifactAliases(artifact)
		selector := artifactSelectorKey(artifact)
		if previous, exists := seenSelectors[selector]; exists {
			return nil, fmt.Errorf("packslip artifacts %q and %q have duplicate selectors", previous, artifact.Name)
		}
		seenSelectors[selector] = artifact.Name
		for field, value := range map[string]*string{
			"os": artifact.OS, "arch": artifact.Arch, "libc": artifact.LibC, "variant": artifact.Variant,
		} {
			if value != nil && !validToken(*value) {
				return nil, fmt.Errorf("packslip artifact %q has invalid %s token %q", artifact.Name, field, *value)
			}
		}
		if artifact.URL != nil {
			if err := validateHTTPSURL(*artifact.URL); err != nil {
				return nil, fmt.Errorf("packslip artifact %q URL: %w", artifact.Name, err)
			}
		}
		if err := validateArtifactRequirements(artifact); err != nil {
			return nil, err
		}
		if err := validateBins(artifact.Bin); err != nil {
			return nil, fmt.Errorf("packslip artifact %q: %w", artifact.Name, err)
		}
	}
	for _, resource := range predicate.Resources {
		if resource.Kind == "" {
			return nil, errors.New("packslip resource kind is empty")
		}
		sources := 0
		if resource.Archive != nil {
			sources++
		}
		if resource.Asset != nil {
			sources++
		}
		if resource.Repo != nil {
			sources++
		}
		if len(resource.Exec) > 0 {
			sources++
		}
		if sources != 1 {
			return nil, fmt.Errorf("packslip resource %q must have exactly one source", resource.Kind)
		}
		if resource.Asset != nil {
			if _, ok := byName[*resource.Asset]; !ok {
				return nil, fmt.Errorf("packslip resource asset %q has no subject", *resource.Asset)
			}
		}
		for field, value := range map[string]*string{
			"os": resource.OS, "arch": resource.Arch, "libc": resource.LibC,
		} {
			if value != nil && !validToken(*value) {
				return nil, fmt.Errorf("packslip resource has invalid %s token %q", field, *value)
			}
		}
		if resource.URL != nil {
			if resource.Asset == nil {
				return nil, errors.New("packslip resource URL is only valid with an asset source")
			}
			if err := validateHTTPSURL(*resource.URL); err != nil {
				return nil, fmt.Errorf("packslip resource asset URL: %w", err)
			}
		}
	}
	allowedSubjects := make(map[string]bool, len(predicate.Artifacts)+len(predicate.Resources))
	for name := range seenArtifacts {
		allowedSubjects[name] = true
	}
	for _, resource := range predicate.Resources {
		if resource.Asset != nil {
			allowedSubjects[*resource.Asset] = true
		}
	}
	if len(allowedSubjects) != len(byName) {
		return nil, errors.New("packslip subjects must correspond exactly to artifacts and asset resources")
	}
	for name := range byName {
		if !allowedSubjects[name] {
			return nil, fmt.Errorf("packslip subject %q is not an artifact or asset resource", name)
		}
	}
	return &parsedRelease{envelope: envelope, predicate: predicate, byName: byName, attested: attested}, nil
}

func validateIdentityShape(identity releaseIdentity) error {
	if identity.Scheme != "sigstore-oidc" {
		return fmt.Errorf("unsupported packslip signer scheme %q", identity.Scheme)
	}
	if strings.TrimSpace(identity.KeyID) == "" {
		return errors.New("packslip signer key_id is empty")
	}
	if identity.Issuer == nil || *identity.Issuer == "" {
		return errors.New("packslip Sigstore OIDC identity has no issuer")
	}
	return nil
}

func validateSubjectDigest(entry subject) error {
	digest, ok := entry.Digest["sha256"]
	if !ok || !validHexDigest(digest, 64) {
		return fmt.Errorf("packslip subject %q must have a lowercase SHA-256 digest", entry.Name)
	}
	for algorithm := range entry.Digest {
		if algorithm != "sha256" && algorithm != "sha512" {
			return fmt.Errorf("packslip subject %q has an unsupported digest algorithm %q", entry.Name, algorithm)
		}
	}
	if sha512, exists := entry.Digest["sha512"]; exists && !validHexDigest(sha512, 128) {
		return fmt.Errorf("packslip subject %q has an invalid SHA-512 digest", entry.Name)
	}
	return nil
}

func validateArtifactRequirements(artifact *releaseArtifact) error {
	if artifact.Requires == nil {
		return nil
	}
	r := artifact.Requires
	for name, value := range map[string]*string{"os_min": r.OSMin, "glibc_min": r.GLibCMin} {
		if value != nil && (strings.TrimSpace(*value) == "" || strings.ContainsAny(*value, " \t\r\n")) {
			return fmt.Errorf("packslip artifact %q has invalid requires.%s", artifact.Name, name)
		}
	}
	if r.Libs != nil {
		seen := map[string]bool{}
		for _, lib := range *r.Libs {
			if !validRequirementName(lib) || seen[lib] {
				return fmt.Errorf("packslip artifact %q has invalid or duplicate required library %q", artifact.Name, lib)
			}
			seen[lib] = true
		}
	}
	seen := map[string]bool{}
	for _, bin := range r.Bins {
		if !validRequirementName(bin.Name) || strings.HasSuffix(strings.ToLower(bin.Name), ".exe") || seen[bin.Name] {
			return fmt.Errorf("packslip artifact %q has invalid or duplicate required command %q", artifact.Name, bin.Name)
		}
		seen[bin.Name] = true
		if bin.Min != nil && !validMinimumVersion(*bin.Min) {
			return fmt.Errorf("packslip artifact %q has invalid minimum version for %q", artifact.Name, bin.Name)
		}
	}
	return nil
}

func validateBins(bins []json.RawMessage) error {
	for _, raw := range bins {
		var path string
		if err := json.Unmarshal(raw, &path); err == nil {
			if path == "" {
				return errors.New("bin path is empty")
			}
			continue
		}
		var named struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err := decodeWire(raw, &named); err != nil {
			return fmt.Errorf("invalid bin entry: %w", err)
		}
		if named.Path == "" || named.Name == "" || strings.Contains(named.Name, "/") {
			return errors.New("bin entry has an invalid path or command name")
		}
	}
	return nil
}

func validRequirementName(value string) bool {
	return value != "" && !strings.ContainsAny(value, "/\\\x00 \t\r\n")
}

func validMinimumVersion(value string) bool {
	if value == "" || value[0] < '0' || value[0] > '9' {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || strings.ContainsRune(".-+", ch)) {
			return false
		}
	}
	return true
}

func validToken(value string) bool { return tokenPattern.MatchString(value) }

func validHexDigest(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func parseUTC(field, value string) (time.Time, error) {
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("packslip %s is not RFC 3339: %w", field, err)
	}
	_, offset := timestamp.Zone()
	if offset != 0 {
		return time.Time{}, fmt.Errorf("packslip %s must be UTC", field)
	}
	return timestamp, nil
}

func validateHTTPSURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("URL must be an absolute HTTPS URL without credentials or fragment")
	}
	return nil
}
