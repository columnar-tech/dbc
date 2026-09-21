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
	"errors"
	"fmt"

	"github.com/columnar-tech/dbc/internal/sourceidentity"
)

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
	projectKey, err := sourceidentity.Parse(sourceidentity.Packslip, predicate.Project)
	if err != nil {
		return nil, fmt.Errorf("release-list project %q is invalid: %w", predicate.Project, err)
	}
	canonicalProject := projectKey.Reference
	expectedKey, err := sourceidentity.Parse(sourceidentity.Packslip, expectedProject)
	if err != nil {
		return nil, err
	}
	expectedProject = expectedKey.Reference
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
