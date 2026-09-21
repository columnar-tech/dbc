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

import "encoding/json"

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
	Name              string                     `json:"name"`
	OS                *string                    `json:"os,omitempty"`
	Arch              *string                    `json:"arch,omitempty"`
	LibC              *string                    `json:"libc,omitempty"`
	Variant           *string                    `json:"variant,omitempty"`
	Size              *uint64                    `json:"size"`
	URL               *string                    `json:"url,omitempty"`
	Format            *string                    `json:"format"`
	Bin               []json.RawMessage          `json:"bin,omitempty"`
	Requires          *requirements              `json:"requires,omitempty"`
	Provenance        []string                   `json:"provenance,omitempty"`
	Extensions        map[string]json.RawMessage `json:"extensions,omitempty"`
	dbcPackageVersion int
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
	driverID  string
}

type parsedList struct {
	envelope  statementEnvelope
	predicate releaseListPredicate
	byURL     map[string]subject
}
