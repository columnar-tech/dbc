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
	"math"
	"strings"

	"github.com/columnar-tech/dbc/internal/resolution"
)

func selectArtifact(release *parsedRelease, target Target) (*releaseArtifact, error) {
	eligible := *release
	eligible.predicate.Artifacts = make([]releaseArtifact, 0, len(release.predicate.Artifacts))
	for _, artifact := range release.predicate.Artifacts {
		if artifact.dbcPackageVersion == 2 && supportedArchiveFormat(stringValue(artifact.Format)) {
			eligible.predicate.Artifacts = append(eligible.predicate.Artifacts, artifact)
		}
	}
	return selectArtifactByFormats(&eligible, target, supportedArchiveFormats)
}

// selectArtifactByFormats applies Packslip's selector and specificity rules,
// using the caller's ordered list to break ties between equally specific
// formats. It intentionally does not impose dbc's archive-format policy.
func selectArtifactByFormats(release *parsedRelease, target Target, formats []string) (*releaseArtifact, error) {
	target = canonicalizeTargetAliases(target)
	if err := resolution.ValidateConcreteTarget(target); err != nil {
		return nil, err
	}
	bestSpecificity, bestFormat := -1, len(formats)
	var chosen, tied *releaseArtifact
	for i := range release.predicate.Artifacts {
		artifact := &release.predicate.Artifacts[i]
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

// validateSupportedArtifactSet ensures the release inventory contains at
// least one artifact that declares dbc package support and an installable
// archive format. Selection ambiguity is checked only for concrete targets.
func validateSupportedArtifactSet(release *parsedRelease) error {
	count := 0
	for i := range release.predicate.Artifacts {
		if release.predicate.Artifacts[i].dbcPackageVersion == 2 && supportedArchiveFormat(stringValue(release.predicate.Artifacts[i].Format)) {
			count++
		}
	}
	if count == 0 {
		return errors.New("packslip release has no dbc artifact with a supported tar.gz or tgz format")
	}
	return nil
}

func convertSelectedArtifact(release *parsedRelease, artifact *releaseArtifact, assets []githubAsset, target Target) (resolution.Artifact, error) {
	artifactURL := stringValue(artifact.URL)
	if artifactURL == "" {
		matches := make([]string, 0, 1)
		for _, asset := range assets {
			if asset.Name == artifact.Name && asset.BrowserDownloadURL != "" {
				matches = append(matches, asset.BrowserDownloadURL)
			}
		}
		if len(matches) != 1 {
			return resolution.Artifact{}, fmt.Errorf("packslip artifact %q has no unambiguous matching GitHub release asset", artifact.Name)
		}
		artifactURL = matches[0]
	}
	if err := validateHTTPSURL(artifactURL); err != nil {
		return resolution.Artifact{}, fmt.Errorf("packslip artifact %q URL: %w", artifact.Name, err)
	}
	return convertArtifact(release, artifact, artifactURL, target)
}

func artifactSelectorKey(artifact *releaseArtifact) string {
	return strings.Join([]string{
		stringValue(artifact.OS), stringValue(artifact.Arch), stringValue(artifact.LibC),
		stringValue(artifact.Variant), stringValue(artifact.Format),
	}, "|")
}

func canonicalizeArtifactAliases(artifact *releaseArtifact) {
	if artifact.OS != nil {
		osName, _ := canonicalizeOSArchAliases(*artifact.OS, "")
		artifact.OS = &osName
	}
	if artifact.Arch != nil {
		_, arch := canonicalizeOSArchAliases("", *artifact.Arch)
		artifact.Arch = &arch
	}
}

func canonicalizeTargetAliases(target Target) Target {
	target.OS, target.Arch = canonicalizeOSArchAliases(target.OS, target.Arch)
	return target
}

func canonicalizeOSArchAliases(osName, arch string) (string, string) {
	if osName == "darwin" {
		osName = "macos"
	}
	switch arch {
	case "x86_64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	}
	return osName, arch
}

func fitsTarget(value *string, target string) bool {
	if value == nil {
		return true
	}
	return target != "" && *value == target
}

func convertArtifact(release *parsedRelease, artifact *releaseArtifact, artifactURL string, target Target) (resolution.Artifact, error) {
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
		Target:         resolution.CanonicalTarget(target),
		Format:         stringValue(artifact.Format),
		PackageVersion: artifact.dbcPackageVersion,
		Location:       resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: artifactURL},
		Hash:           "sha256:" + subject.Digest["sha256"],
		Size:           &size,
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
