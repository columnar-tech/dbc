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
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/columnar-tech/dbc/internal/resolution"
)

var (
	semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	tokenPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
)

type statementEnvelope struct {
	Type          string          `json:"_type"`
	Subject       []subject       `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     json.RawMessage `json:"predicate"`
}

type subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

type sourceMetadata struct {
	Repo   string  `json:"repo"`
	Commit *string `json:"commit,omitempty"`
	Tag    *string `json:"tag,omitempty"`
}

type releaseIdentity struct {
	Scheme string  `json:"scheme"`
	KeyID  string  `json:"key_id"`
	Issuer *string `json:"issuer,omitempty"`
}

type releasePredicate struct {
	Project     string                     `json:"project"`
	Version     string                     `json:"version"`
	PublishedAt string                     `json:"published_at"`
	Source      *sourceMetadata            `json:"source,omitempty"`
	Artifacts   []releaseArtifact          `json:"artifacts"`
	Resources   []releaseResource          `json:"resources,omitempty"`
	Identity    releaseIdentity            `json:"identity"`
	AttestedBy  string                     `json:"attested_by,omitempty"`
	NotesURL    string                     `json:"notes_url,omitempty"`
	Extensions  map[string]json.RawMessage `json:"extensions,omitempty"`
}

type releaseArtifact struct {
	Name       string                     `json:"name"`
	OS         *string                    `json:"os,omitempty"`
	Arch       *string                    `json:"arch,omitempty"`
	LibC       *string                    `json:"libc,omitempty"`
	Variant    *string                    `json:"variant,omitempty"`
	Size       *uint64                    `json:"size"`
	URL        *string                    `json:"url,omitempty"`
	Format     *string                    `json:"format"`
	Bin        []json.RawMessage          `json:"bin,omitempty"`
	Requires   *requirements              `json:"requires,omitempty"`
	Provenance []string                   `json:"provenance,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

type requirements struct {
	OSMin    *string           `json:"os_min,omitempty"`
	GLibCMin *string           `json:"glibc_min,omitempty"`
	Libs     *[]string         `json:"libs,omitempty"`
	Bins     []requiredCommand `json:"bin,omitempty"`
}

type requiredCommand struct {
	Name string  `json:"name"`
	Min  *string `json:"min,omitempty"`
}

type releaseResource struct {
	Kind       string                     `json:"kind"`
	Artifact   *string                    `json:"artifact,omitempty"`
	OS         *string                    `json:"os,omitempty"`
	Arch       *string                    `json:"arch,omitempty"`
	LibC       *string                    `json:"libc,omitempty"`
	Shell      *string                    `json:"shell,omitempty"`
	Shells     []string                   `json:"shells,omitempty"`
	Name       *string                    `json:"name,omitempty"`
	Bin        *string                    `json:"bin,omitempty"`
	Format     *string                    `json:"format,omitempty"`
	Archive    *string                    `json:"archive,omitempty"`
	Asset      *string                    `json:"asset,omitempty"`
	URL        *string                    `json:"url,omitempty"`
	Repo       *string                    `json:"repo,omitempty"`
	Exec       []string                   `json:"exec,omitempty"`
	Env        map[string]string          `json:"env,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

type releaseListPredicate struct {
	Project    string                     `json:"project"`
	Generated  string                     `json:"generated_at"`
	Expires    string                     `json:"expires_at"`
	Sequence   *uint64                    `json:"sequence"`
	Identity   releaseIdentity            `json:"identity"`
	Latest     *string                    `json:"latest,omitempty"`
	Releases   []releaseRef               `json:"releases"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

type releaseRef struct {
	Version      string                     `json:"version"`
	Tag          *string                    `json:"tag,omitempty"`
	PublishedAt  string                     `json:"published_at"`
	Packslip     string                     `json:"packslip"`
	Status       string                     `json:"status,omitempty"`
	StatusReason *string                    `json:"status_reason,omitempty"`
	Security     bool                       `json:"security,omitempty"`
	Extensions   map[string]json.RawMessage `json:"extensions,omitempty"`
	Evidence     []releaseEvidence          `json:"evidence,omitempty"`
}

type releaseEvidence struct {
	Kind   string  `json:"kind"`
	Detail *string `json:"detail,omitempty"`
}

type parsedRelease struct {
	envelope  statementEnvelope
	predicate releasePredicate
	byName    map[string]subject
	attested  string
}

type parsedList struct {
	envelope  statementEnvelope
	predicate releaseListPredicate
	byURL     map[string]subject
}

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
	canonicalProject, err := normalizeProject(predicate.Project)
	if err != nil {
		return nil, fmt.Errorf("packslip project %q is invalid: %w", predicate.Project, err)
	}
	if expectedProject != "" {
		expectedProject, err = normalizeProject(expectedProject)
		if err != nil {
			return nil, err
		}
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

func parseList(payload []byte, expectedProject string) (*parsedList, error) {
	var envelope statementEnvelope
	if err := decodeWire(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode packslip release list: %w", err)
	}
	if envelope.Type != StatementType {
		return nil, fmt.Errorf("release list _type %q is unsupported", envelope.Type)
	}
	if envelope.PredicateType != ListPredicateType {
		return nil, fmt.Errorf("release list predicateType %q is not %s", envelope.PredicateType, ListPredicateType)
	}
	var predicate releaseListPredicate
	if err := decodeWire(envelope.Predicate, &predicate); err != nil {
		return nil, fmt.Errorf("decode packslip release-list predicate: %w", err)
	}
	canonicalProject, err := normalizeProject(predicate.Project)
	if err != nil {
		return nil, fmt.Errorf("release-list project %q is invalid: %w", predicate.Project, err)
	}
	expectedProject, err = normalizeProject(expectedProject)
	if err != nil {
		return nil, err
	}
	if canonicalProject != expectedProject {
		return nil, fmt.Errorf("release-list project %q does not match requested project %q", predicate.Project, expectedProject)
	}
	predicate.Project = canonicalProject
	generated, err := parseUTC("generated_at", predicate.Generated)
	if err != nil {
		return nil, err
	}
	expires, err := parseUTC("expires_at", predicate.Expires)
	if err != nil {
		return nil, err
	}
	if !expires.After(generated) {
		return nil, errors.New("release-list expires_at must be after generated_at")
	}
	if predicate.Sequence == nil {
		return nil, errors.New("release-list sequence is missing")
	}
	if len(predicate.Releases) == 0 {
		return nil, errors.New("release list has no releases")
	}
	if err := validateIdentityShape(predicate.Identity); err != nil {
		return nil, err
	}
	byURL := make(map[string]subject, len(envelope.Subject))
	for _, entry := range envelope.Subject {
		if entry.Name == "" {
			return nil, errors.New("release-list subject URL is empty")
		}
		if _, exists := byURL[entry.Name]; exists {
			return nil, fmt.Errorf("release list has duplicate subject %q", entry.Name)
		}
		if err := validateSubjectDigest(entry); err != nil {
			return nil, err
		}
		byURL[entry.Name] = entry
	}
	seenVersion := make(map[string]bool, len(predicate.Releases))
	seenURL := make(map[string]bool, len(predicate.Releases))
	for _, ref := range predicate.Releases {
		if !semverPattern.MatchString(ref.Version) {
			return nil, fmt.Errorf("release-list version %q is not SemVer 2.0.0", ref.Version)
		}
		if seenVersion[ref.Version] {
			return nil, fmt.Errorf("release list has duplicate version %q", ref.Version)
		}
		seenVersion[ref.Version] = true
		if _, err := parseUTC("release published_at", ref.PublishedAt); err != nil {
			return nil, err
		}
		if err := validateHTTPSURL(ref.Packslip); err != nil {
			return nil, fmt.Errorf("release-list packslip URL: %w", err)
		}
		if seenURL[ref.Packslip] {
			return nil, fmt.Errorf("release list has duplicate packslip URL %q", ref.Packslip)
		}
		seenURL[ref.Packslip] = true
		if _, ok := byURL[ref.Packslip]; !ok {
			return nil, fmt.Errorf("release-list packslip URL %q has no subject", ref.Packslip)
		}
		if ref.Status != "" && ref.Status != "yanked" {
			return nil, fmt.Errorf("unsupported release-list status %q", ref.Status)
		}
		if ref.StatusReason != nil && ref.Status != "yanked" {
			return nil, fmt.Errorf("status_reason is only valid for a yanked release")
		}
		for _, evidence := range ref.Evidence {
			if evidence.Kind == "" {
				return nil, errors.New("release-list evidence kind is empty")
			}
		}
	}
	if len(byURL) != len(seenURL) {
		return nil, errors.New("release-list subjects must correspond exactly to listed packslip bundles")
	}
	if predicate.Latest != nil && !seenVersion[*predicate.Latest] {
		return nil, fmt.Errorf("release-list latest version %q is not listed", *predicate.Latest)
	}
	return &parsedList{envelope: envelope, predicate: predicate, byURL: byURL}, nil
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

func decodeStrict(data []byte, destination any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// decodeWire keeps the structural protections required for packslip payloads
// while allowing future optional fields to be ignored by this consumer.
// Trust-store files intentionally continue to use decodeStrict.
func decodeWire(data []byte, destination any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = true
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

// peekBundleDigest reads only the digest needed to invoke the cryptographic
// verifier. Its result is untrusted and is never used to establish identity or
// accept metadata; strict parsing happens only after Verify succeeds.
func peekBundleDigest(bundleJSON []byte) (string, string, []byte, error) {
	var raw struct {
		Envelope struct {
			Payload     string `json:"payload"`
			PayloadType string `json:"payloadType"`
		} `json:"dsseEnvelope"`
	}
	if err := json.Unmarshal(bundleJSON, &raw); err != nil {
		return "", "", nil, fmt.Errorf("decode Sigstore bundle envelope: %w", err)
	}
	if raw.Envelope.PayloadType != "application/vnd.in-toto+json" {
		return "", "", nil, fmt.Errorf("Sigstore bundle payload type %q is not in-toto JSON", raw.Envelope.PayloadType)
	}
	payload, err := base64.StdEncoding.DecodeString(raw.Envelope.Payload)
	if err != nil {
		return "", "", nil, fmt.Errorf("decode Sigstore bundle payload: %w", err)
	}
	var candidate struct {
		Subject []subject `json:"subject"`
	}
	if err := json.Unmarshal(payload, &candidate); err != nil {
		return "", "", nil, fmt.Errorf("peek Sigstore statement subject: %w", err)
	}
	for _, entry := range candidate.Subject {
		if digest, ok := entry.Digest["sha256"]; ok && validHexDigest(digest, 64) {
			return "sha256", digest, payload, nil
		}
		if digest, ok := entry.Digest["sha512"]; ok && validHexDigest(digest, 128) {
			return "sha512", digest, payload, nil
		}
	}
	return "", "", nil, errors.New("Sigstore statement has no supported SHA-256 or SHA-512 subject for verification")
}

func normalizeProject(value string) (string, error) {
	if value != strings.TrimSpace(value) || strings.Contains(value, "://") || strings.HasSuffix(value, "/") {
		return "", fmt.Errorf("project must be a canonical host path without URL scheme: %q", value)
	}
	parts := strings.Split(value, "/")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "github.com") {
		return "", fmt.Errorf("only GitHub projects of the form github.com/owner/repo are supported: %q", value)
	}
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("project contains an invalid path segment: %q", value)
		}
		for _, ch := range part {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("._-", ch)) {
				return "", fmt.Errorf("project contains an invalid path segment: %q", value)
			}
		}
		if i < 3 {
			parts[i] = strings.ToLower(part)
		}
	}
	return strings.Join(parts, "/"), nil
}

func projectParts(project string) (owner, repo string, tool []string) {
	parts := strings.Split(project, "/")
	return parts[1], parts[2], parts[3:]
}

func tagVersion(tag, project string) (string, bool) {
	_, repo, tool := projectParts(project)
	prefixes := make([]string, 0, 3)
	if len(tool) > 0 {
		prefixes = append(prefixes, strings.Join(tool, "/"))
		if len(tool) > 1 {
			prefixes = append(prefixes, tool[len(tool)-1])
		}
	}
	prefixes = append(prefixes, repo)
	rest := tag
	for _, prefix := range prefixes {
		for _, separator := range []string{"/", "-", "_", "@"} {
			if strings.HasPrefix(rest, prefix+separator) {
				rest = strings.TrimPrefix(rest, prefix+separator)
				goto prefixFound
			}
		}
	}
prefixFound:
	rest = strings.TrimPrefix(rest, "v")
	version, ok := normalizeLooseTagVersion(rest)
	if !ok || !semverPattern.MatchString(version) {
		return "", false
	}
	return version, true
}

func normalizeLooseTagVersion(value string) (string, bool) {
	coreEnd := strings.IndexAny(value, "-+")
	core, suffix := value, ""
	if coreEnd >= 0 {
		core, suffix = value[:coreEnd], value[coreEnd:]
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return "", false
	}
	for i, part := range parts {
		if part == "" {
			return "", false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return "", false
			}
		}
		trimmed := strings.TrimLeft(part, "0")
		if trimmed == "" {
			trimmed = "0"
		}
		parts[i] = trimmed
	}
	if len(parts) == 2 {
		parts = append(parts, "0")
	}
	version := strings.Join(parts, ".") + suffix
	return version, semverPattern.MatchString(version)
}

func selectArtifact(release *parsedRelease, target Target) (*releaseArtifact, error) {
	formats := supportedArchiveFormats
	bestSpecificity, bestFormat := -1, len(formats)
	var chosen, tied *releaseArtifact
	for i := range release.predicate.Artifacts {
		artifact := &release.predicate.Artifacts[i]
		if !supportedArchiveFormat(stringValue(artifact.Format)) {
			continue
		}
		if !fitsTarget(artifact.OS, target.OS) || !fitsTarget(artifact.Arch, target.Arch) || !fitsTarget(artifact.LibC, target.LibC) {
			continue
		}
		if artifact.Variant == nil && target.Variant != "" || artifact.Variant != nil && *artifact.Variant != target.Variant {
			continue
		}
		formatIndex := -1
		for index, format := range formats {
			if *artifact.Format == format {
				formatIndex = index
				break
			}
		}
		if formatIndex < 0 {
			continue
		}
		specificity := 0
		for _, value := range []*string{artifact.OS, artifact.Arch, artifact.LibC} {
			if value != nil {
				specificity++
			}
		}
		if specificity > bestSpecificity || specificity == bestSpecificity && formatIndex < bestFormat {
			chosen, tied = artifact, nil
			bestSpecificity, bestFormat = specificity, formatIndex
		} else if specificity == bestSpecificity && formatIndex == bestFormat {
			tied = artifact
		}
	}
	if chosen == nil {
		return nil, ErrReleaseNotFound
	}
	if tied != nil {
		return nil, fmt.Errorf("%w: %q and %q", ErrAmbiguousArtifact, chosen.Name, tied.Name)
	}
	return chosen, nil
}

var supportedArchiveFormats = []string{"tar.gz", "tgz"}

func supportedArchiveFormat(format string) bool {
	return format == "tar.gz" || format == "tgz"
}

// validateSupportedArtifactSet checks ambiguity over the entire signed release,
// including platforms other than the one currently requested.
func validateSupportedArtifactSet(release *parsedRelease) error {
	count := 0
	for i := range release.predicate.Artifacts {
		left := &release.predicate.Artifacts[i]
		if !supportedArchiveFormat(stringValue(left.Format)) {
			continue
		}
		count++
		for j := i + 1; j < len(release.predicate.Artifacts); j++ {
			right := &release.predicate.Artifacts[j]
			if !supportedArchiveFormat(stringValue(right.Format)) || stringValue(left.Format) != stringValue(right.Format) {
				continue
			}
			if artifactSpecificity(left) == artifactSpecificity(right) && selectorVariantsOverlap(left, right) && selectorsOverlap(left, right) {
				return fmt.Errorf("%w: %q and %q", ErrAmbiguousArtifact, left.Name, right.Name)
			}
		}
	}
	if count == 0 {
		return errors.New("packslip release has no supported tar.gz or tgz artifacts")
	}
	return nil
}

func artifactSpecificity(artifact *releaseArtifact) int {
	specificity := 0
	for _, selector := range []*string{artifact.OS, artifact.Arch, artifact.LibC} {
		if selector != nil {
			specificity++
		}
	}
	return specificity
}

func selectorVariantsOverlap(left, right *releaseArtifact) bool {
	if left.Variant == nil || right.Variant == nil {
		return left.Variant == nil && right.Variant == nil
	}
	return *left.Variant == *right.Variant
}

func selectorsOverlap(left, right *releaseArtifact) bool {
	for _, pair := range [][2]*string{{left.OS, right.OS}, {left.Arch, right.Arch}, {left.LibC, right.LibC}} {
		if pair[0] != nil && pair[1] != nil && *pair[0] != *pair[1] {
			return false
		}
	}
	return true
}

func convertSupportedArtifacts(release *parsedRelease, assets []githubAsset) ([]resolution.Artifact, error) {
	result := make([]resolution.Artifact, 0, len(release.predicate.Artifacts))
	for i := range release.predicate.Artifacts {
		artifact := &release.predicate.Artifacts[i]
		if !supportedArchiveFormat(stringValue(artifact.Format)) {
			continue
		}
		artifactURL := stringValue(artifact.URL)
		if artifactURL == "" {
			matches := make([]string, 0, 1)
			for _, asset := range assets {
				if asset.Name == artifact.Name && asset.BrowserDownloadURL != "" {
					matches = append(matches, asset.BrowserDownloadURL)
				}
			}
			if len(matches) != 1 {
				return nil, fmt.Errorf("packslip artifact %q has no unambiguous matching GitHub release asset", artifact.Name)
			}
			artifactURL = matches[0]
		}
		if err := validateHTTPSURL(artifactURL); err != nil {
			return nil, fmt.Errorf("packslip artifact %q URL: %w", artifact.Name, err)
		}
		converted, err := convertArtifact(release, artifact, artifactURL)
		if err != nil {
			return nil, err
		}
		result = append(result, converted)
	}
	return result, nil
}

func artifactSelectorKey(artifact *releaseArtifact) string {
	return strings.Join([]string{
		stringValue(artifact.OS), stringValue(artifact.Arch), stringValue(artifact.LibC),
		stringValue(artifact.Variant), stringValue(artifact.Format),
	}, "|")
}

func fitsTarget(value *string, target string) bool {
	if value == nil {
		return true
	}
	return target != "" && *value == target
}

func convertArtifact(release *parsedRelease, artifact *releaseArtifact, artifactURL string) (resolution.Artifact, error) {
	if artifact.Size == nil || *artifact.Size > math.MaxInt64 {
		return resolution.Artifact{}, fmt.Errorf("packslip artifact %q size exceeds dbc's supported range", artifact.Name)
	}
	subject, ok := release.byName[artifact.Name]
	if !ok {
		return resolution.Artifact{}, fmt.Errorf("packslip artifact %q has no subject", artifact.Name)
	}
	if artifactURL == "" {
		return resolution.Artifact{}, fmt.Errorf("packslip artifact %q has no resolved URL", artifact.Name)
	}
	size := int64(*artifact.Size)
	result := resolution.Artifact{
		OS:      stringValue(artifact.OS),
		Arch:    stringValue(artifact.Arch),
		LibC:    stringValue(artifact.LibC),
		Variant: stringValue(artifact.Variant),
		Format:  stringValue(artifact.Format),
		URL:     artifactURL,
		Hash:    "sha256:" + subject.Digest["sha256"],
		Size:    &size,
	}
	if artifact.Requires != nil {
		requires := artifact.Requires
		result.HostRequirements.OSMin = stringValue(requires.OSMin)
		result.HostRequirements.GLibCMin = stringValue(requires.GLibCMin)
		if requires.Libs != nil {
			result.HostRequirements.Libs = append([]string(nil), (*requires.Libs)...)
		}
		for _, bin := range requires.Bins {
			result.HostRequirements.Bins = append(result.HostRequirements.Bins, resolution.NamedRequirement{Name: bin.Name, Min: stringValue(bin.Min)})
		}
	}
	if err := resolution.ValidateArtifactMetadata(result.Hash, result.Size); err != nil {
		return resolution.Artifact{}, err
	}
	return result, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
