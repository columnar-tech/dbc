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

// Package packslip resolves GitHub-hosted packslip/v1 release statements into
// dbc's source-independent resolution model.
package packslip

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/columnar-tech/dbc/internal/packslipverify"
	"github.com/columnar-tech/dbc/internal/resolution"
)

const (
	ReleasePredicateType = "https://packslip.dev/release/v1"
	ListPredicateType    = "https://packslip.dev/releases/v1"
	StatementType        = "https://in-toto.io/Statement/v1"
	GitHubOIDCIssuer     = "https://token.actions.githubusercontent.com"
)

var (
	ErrUnsupported       = errors.New("packslip resolution is unsupported on this target")
	ErrReleaseNotFound   = errors.New("packslip release was not found")
	ErrYankedRelease     = errors.New("packslip release is yanked")
	ErrAmbiguousArtifact = errors.New("packslip artifact selection is ambiguous")
)

// PackslipSource identifies a project without tying the core model to a
// transport. GitHub is the first discovery implementation.
type PackslipSource struct {
	Project string
}

// Target is the canonical host tuple used to select one archive from a release.
type Target = resolution.Target

// Request identifies one driver release and the archive needed by this host.
type Request struct {
	DriverID string
	Version  string
	Target   Target
}

// TrustStore retains the identities and signed-list state accepted by dbc.
// Its native implementation persists state under dbc's user config path.
type TrustStore struct {
	path string
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Draft   bool          `json:"draft"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Config controls release discovery. Empty GitHub base URLs use the public
// GitHub endpoints; custom endpoints are primarily useful for enterprise
// deployments and deterministic tests.
type Config struct {
	HTTPClient       *http.Client
	Verifier         packslipverify.PackslipVerifier
	TrustedRootJSON  []byte
	TrustStore       *TrustStore
	GitHubAPIBaseURL string
	GitHubRawBaseURL string
	Now              func() time.Time
}

// Resolver resolves authenticated release metadata for one target.
type Resolver interface {
	Resolve(context.Context, PackslipSource, Request) (resolution.ResolvedRelease, error)
}

type resolutionError struct {
	operation string
	err       error
}

func (e *resolutionError) Error() string { return fmt.Sprintf("%s: %v", e.operation, e.err) }
func (e *resolutionError) Unwrap() error { return e.err }
