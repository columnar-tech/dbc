// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package sourceresolution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/packslip"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceidentity"
)

// Request is the small amount of project context needed to resolve one source.
// BaseDir is the absolute directory containing dbc.toml/dbc.lock. Platform is
// the current config platform tuple used by package v2 metadata.
type Request struct {
	DriverID string
	Version  string // Optional for local path sources; an omitted version is read from package metadata.
	Target   resolution.Target
	Platform string
	BaseDir  string
}

// ResolvePackslip passes the declared project source to the existing signed
// Packslip resolver and checks that its result still matches the request.
func ResolvePackslip(ctx context.Context, resolver packslip.Resolver, project, driverID, version string) (resolution.ResolvedRelease, error) {
	if ctx == nil {
		return resolution.ResolvedRelease{}, errors.New("packslip resolution requires a context")
	}
	if project == "" {
		return resolution.ResolvedRelease{}, errors.New("packslip resolution requires a project")
	}
	if resolver == nil {
		return resolution.ResolvedRelease{}, errors.New("packslip resolver is nil")
	}
	declaredKey, err := sourceidentity.Parse(sourceidentity.Packslip, project)
	if err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("invalid packslip project: %w", err)
	}
	requestedVersion, err := semver.StrictNewVersion(version)
	if err != nil || requestedVersion.String() != version {
		return resolution.ResolvedRelease{}, fmt.Errorf("requested packslip version %q must be an exact SemVer 2.0.0 version", version)
	}

	release, err := resolver.Resolve(ctx, packslip.PackslipSource{Project: project}, packslip.Request{DriverID: driverID, Version: version})
	if err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("resolve packslip source: %w", err)
	}
	if release.DriverID == "" {
		return resolution.ResolvedRelease{}, errors.New("packslip release has no signed dbc driver ID")
	}
	if driverID != "" && release.DriverID != driverID {
		return resolution.ResolvedRelease{}, fmt.Errorf("packslip release driver ID %q does not match requested driver %q", release.DriverID, driverID)
	}
	if release.Version != version {
		return resolution.ResolvedRelease{}, fmt.Errorf("packslip release version %q does not match requested version %q", release.Version, version)
	}
	lockedKey, identityErr := sourceidentity.Parse(sourceidentity.Packslip, release.Source.Reference)
	if release.Source.Type != string(sourceidentity.Packslip) || identityErr != nil || lockedKey != declaredKey {
		return resolution.ResolvedRelease{}, fmt.Errorf("packslip release source identity %q does not match declared project %q", release.Source.Reference, declaredKey.Reference)
	}
	if err := resolution.ValidateResolvedRelease(release); err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("packslip resolver returned an invalid release: %w", err)
	}
	for i, artifact := range release.Artifacts {
		if artifact.PackageVersion != 0 {
			return resolution.ResolvedRelease{}, fmt.Errorf("packslip artifact %d package format must remain unspecified until archive inspection", i)
		}
		if artifact.Size == nil {
			return resolution.ResolvedRelease{}, fmt.Errorf("packslip artifact %d must declare archive size", i)
		}
	}
	return release, nil
}

// ResolvePath inspects a local package archive and returns a release containing
// only the current target. An omitted requested version is derived from package
// metadata. The declared path remains unchanged in source identity and artifact
// location; filesystem access is relative to BaseDir.
func ResolvePath(ctx context.Context, declaredPath string, request Request) (resolution.ResolvedRelease, error) {
	if request.DriverID == "" {
		return resolution.ResolvedRelease{}, errors.New("path resolution requires a driver ID")
	}
	if request.Version != "" {
		version, err := semver.StrictNewVersion(request.Version)
		if err != nil || version.String() != request.Version {
			return resolution.ResolvedRelease{}, fmt.Errorf("requested path package version %q must be an exact SemVer 2.0.0 version", request.Version)
		}
	}
	target := resolution.CanonicalTarget(request.Target)
	if err := resolution.ValidateConcreteTarget(target); err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("invalid current target: %w", err)
	}
	if request.Platform == "" {
		return resolution.ResolvedRelease{}, errors.New("path resolution requires the current platform tuple")
	}
	platformTarget, err := resolution.TargetFromPlatformTuple(request.Platform)
	if err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("invalid current platform tuple: %w", err)
	}
	if platformTarget != target {
		return resolution.ResolvedRelease{}, fmt.Errorf("current target %v does not match platform tuple %q", target, request.Platform)
	}
	if !filepath.IsAbs(request.BaseDir) {
		return resolution.ResolvedRelease{}, errors.New("project base directory must be absolute")
	}
	location := resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: declaredPath}
	if err := resolution.ValidateArtifactLocation(location); err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("invalid path source: %w", err)
	}
	opened, err := OpenArtifact(ctx, nil, location, request.BaseDir)
	if err != nil {
		return resolution.ResolvedRelease{}, err
	}
	defer opened.Close()

	archiveHash, archiveSize, err := hashFile(opened.File)
	if err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("hash local package archive: %w", err)
	}
	manifest, err := config.InspectPackageMetadata(opened.File)
	if err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("inspect local package archive: %w", err)
	}
	if manifest.Version == nil {
		return resolution.ResolvedRelease{}, errors.New("local package metadata has no version")
	}
	resolvedVersion := manifest.Version.String()
	if request.Version != "" && resolvedVersion != request.Version {
		return resolution.ResolvedRelease{}, fmt.Errorf("local package version %q does not match requested version %q", resolvedVersion, request.Version)
	}
	if manifest.PackageVersion == 2 {
		if manifest.ID != request.DriverID {
			return resolution.ResolvedRelease{}, fmt.Errorf("local package ID %q does not match requested driver %q", manifest.ID, request.DriverID)
		}
		if manifest.PackagePlatform != request.Platform {
			return resolution.ResolvedRelease{}, fmt.Errorf("local package platform %q does not match current platform %q", manifest.PackagePlatform, request.Platform)
		}
	}
	// Legacy MANIFEST packages predate package IDs and platform declarations.
	// The existing installer supplies the requested runtime ID and current
	// platform for that format, so the local source adapter uses the same rule.
	format, err := packageFormat(declaredPath)
	if err != nil {
		return resolution.ResolvedRelease{}, err
	}
	size := archiveSize
	release := resolution.ResolvedRelease{
		DriverID: request.DriverID,
		Version:  resolvedVersion,
		Source:   resolution.SourceSpec{Type: "path", Reference: declaredPath},
		Artifacts: []resolution.Artifact{{
			Target:         target,
			Format:         format,
			PackageVersion: manifest.PackageVersion,
			Location:       location,
			Hash:           archiveHash,
			Size:           &size,
		}},
	}
	if err := resolution.ValidateResolvedRelease(release); err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("invalid local package release: %w", err)
	}
	return release, nil
}

func packageFormat(path string) (string, error) {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"):
		return "tar.gz", nil
	case strings.HasSuffix(lower, ".tgz"):
		return "tgz", nil
	default:
		return "", fmt.Errorf("local package archive %q must use .tar.gz or .tgz format", path)
	}
}

func hashFile(file *os.File) (string, int64, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", size, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", size, err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}
