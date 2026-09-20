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

package dbc

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/go-faster/yaml"
)

func resolveRegistryPackageURL(d Driver, version *semver.Version, pkg registryPackage) (*url.URL, error) {
	if pkg.URL != "" {
		uri, err := url.Parse(pkg.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid package URL %q: %w", pkg.URL, err)
		}
		if uri.IsAbs() {
			return uri, nil
		}
		if d.Registry == nil || d.Registry.BaseURL == nil {
			return nil, fmt.Errorf("cannot resolve package URL for %s: driver has no registry URL", d.Title)
		}
		return d.Registry.BaseURL.JoinPath(pkg.URL), nil
	}

	if d.Registry == nil || d.Registry.BaseURL == nil {
		return nil, fmt.Errorf("cannot resolve package URL for %s: driver has no registry URL", d.Title)
	}
	if version == nil {
		return nil, fmt.Errorf("cannot resolve package URL for %s: release has no version", d.Title)
	}

	platform := pkg.PlatformTuple
	return d.Registry.BaseURL.JoinPath(d.Path, version.String(),
		d.Path+"_"+platform+"-"+version.String()+".tar.gz"), nil
}

// resolvedRelease adapts registry metadata into the source-independent model.
// It resolves omitted URLs for every declared platform before returning the
// release, so lock conversion never sees a mix of resolved and implicit URLs.
func (p pkginfo) resolvedRelease(d Driver) (resolution.ResolvedRelease, error) {
	if p.Version == nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("cannot resolve release for %s: release has no version", d.Title)
	}
	if strings.TrimSpace(d.Path) == "" {
		return resolution.ResolvedRelease{}, fmt.Errorf("cannot record registry source identity for release: driver ID is empty")
	}
	if d.Registry == nil || d.Registry.BaseURL == nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("cannot record registry source identity for %s: registry BaseURL is missing", d.Path)
	}
	baseURL := d.Registry.BaseURL
	if !baseURL.IsAbs() ||
		(!strings.EqualFold(baseURL.Scheme, "http") && !strings.EqualFold(baseURL.Scheme, "https")) ||
		baseURL.Hostname() == "" {
		return resolution.ResolvedRelease{}, fmt.Errorf("cannot record registry source identity for %s: registry BaseURL must be an absolute HTTP(S) URL with a host", d.Path)
	}

	release := resolution.ResolvedRelease{
		DriverID: d.Path,
		Version:  p.Version.String(),
		Source: resolution.SourceSpec{
			Type:      "registry",
			Reference: baseURL.String(),
		},
		Artifacts: make([]resolution.Artifact, 0, len(p.Packages)),
	}
	for _, pkg := range p.Packages {
		artifact, err := pkg.resolveArtifact()
		if err != nil {
			return resolution.ResolvedRelease{}, fmt.Errorf("invalid artifact for platform %q: %w", pkg.PlatformTuple, err)
		}
		uri, err := resolveRegistryPackageURL(d, p.Version, pkg)
		if err != nil {
			return resolution.ResolvedRelease{}, err
		}
		artifact.Location = resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: uri.String()}
		if err := resolution.ValidateArtifactLocation(artifact.Location); err != nil {
			return resolution.ResolvedRelease{}, fmt.Errorf("invalid artifact URL for platform %q: %w", pkg.PlatformTuple, err)
		}
		release.Artifacts = append(release.Artifacts, artifact)
	}
	return release, nil
}

func (p registryPackage) resolveArtifact() (resolution.Artifact, error) {
	hash, hashPresent, err := optionalString(p.Hash, "hash")
	if err != nil {
		return resolution.Artifact{}, err
	}
	if hashPresent && hash == "" {
		return resolution.Artifact{}, fmt.Errorf("artifact hash must not be empty")
	}

	var size *int64
	if p.Size.Kind != 0 {
		if p.Size.Kind != yaml.ScalarNode || p.Size.Tag != "!!int" {
			return resolution.Artifact{}, fmt.Errorf("artifact size must be an integer")
		}
		var value int64
		if err := p.Size.Decode(&value); err != nil {
			return resolution.Artifact{}, fmt.Errorf("invalid artifact size: %w", err)
		}
		size = &value
	}

	if err := resolution.ValidateArtifactMetadata(hash, size); err != nil {
		return resolution.Artifact{}, err
	}
	target, err := resolution.TargetFromPlatformTuple(p.PlatformTuple)
	if err != nil {
		return resolution.Artifact{}, err
	}
	return resolution.Artifact{
		Target: target,
		Hash:   hash,
		Size:   size,
	}, nil
}

func optionalString(node yaml.Node, field string) (string, bool, error) {
	if node.Kind == 0 {
		return "", false, nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", true, fmt.Errorf("artifact %s must be a string", field)
	}
	return node.Value, true, nil
}

func validateRegistryMetadata(drivers []Driver) error {
	for _, driver := range drivers {
		for _, release := range driver.PkgInfo {
			for _, pkg := range release.Packages {
				if _, err := pkg.resolveArtifact(); err != nil {
					return fmt.Errorf("invalid artifact metadata for driver %q version %s platform %q: %w",
						driver.Path, release.Version, pkg.PlatformTuple, err)
				}
			}
		}
	}
	return nil
}
