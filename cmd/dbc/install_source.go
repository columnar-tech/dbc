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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceidentity"
	"github.com/columnar-tech/dbc/internal/sourceresolution"
)

func parsePackslipInstallArgument(input string) (project, version string, matched bool, err error) {
	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "github.com/") &&
		!strings.HasPrefix(lower, "https://github.com/") &&
		!strings.HasPrefix(lower, "http://github.com/") {
		return "", "", false, nil
	}
	if trimmed != input {
		return "", "", true, errors.New("Packslip project must be a canonical github.com host path")
	}
	if strings.Contains(trimmed, "://") {
		return "", "", true, errors.New("Packslip install requires a github.com project path, not a URL")
	}
	if strings.Count(trimmed, "=") != 1 {
		return "", "", true, errors.New("Packslip install syntax is github.com/owner/repo[/tool]=<exact-version>")
	}
	parts := strings.SplitN(trimmed, "=", 2)
	project, version = parts[0], parts[1]
	key, parseErr := sourceidentity.Parse(sourceidentity.Packslip, project)
	if parseErr != nil || key.Reference != project {
		return "", "", true, fmt.Errorf("invalid canonical Packslip project %q", project)
	}
	parsed, parseErr := semver.StrictNewVersion(version)
	if parseErr != nil || parsed.String() != version {
		return "", "", true, fmt.Errorf("Packslip version %q must be an exact canonical SemVer 2.0.0 version", version)
	}
	return project, version, true, nil
}

func (m progressiveInstallModel) resolveDirectInstall(ctx context.Context) (installItem, error) {
	platform := config.PlatformTuple()
	target, err := resolution.TargetFromPlatformTuple(platform)
	if err != nil {
		return installItem{}, fmt.Errorf("unsupported current platform: %w", err)
	}
	var release resolution.ResolvedRelease
	switch {
	case m.isPackslip:
		if m.newPackslipResolver == nil {
			return installItem{}, errors.New("Packslip resolver is unavailable")
		}
		resolver, err := m.newPackslipResolver()
		if err != nil {
			return installItem{}, fmt.Errorf("create Packslip resolver: %w", err)
		}
		release, err = sourceresolution.ResolvePackslip(ctx, resolver, m.packslipProject, "", m.packslipVersion)
		if err != nil {
			return installItem{}, err
		}
	case m.isLocal:
		cwd, err := os.Getwd()
		if err != nil {
			return installItem{}, fmt.Errorf("resolve current directory for local package: %w", err)
		}
		inspection, err := os.Open(m.localPackagePath)
		if err != nil {
			return installItem{}, err
		}
		metadata, inspectErr := config.InspectPackageMetadata(inspection)
		closeErr := inspection.Close()
		if inspectErr != nil {
			return installItem{}, inspectErr
		}
		if closeErr != nil {
			return installItem{}, closeErr
		}
		driverID := legacyLocalPackageID(m.localPackagePath, platform)
		if metadata.PackageVersion == 2 {
			driverID = metadata.ID
		}
		release, err = sourceresolution.ResolvePath(ctx, m.localPackagePath, sourceresolution.Request{
			DriverID: driverID,
			Target:   target,
			Platform: platform,
			BaseDir:  cwd,
		})
		if err != nil {
			return installItem{}, err
		}
	default:
		return installItem{}, errors.New("no direct install source was selected")
	}
	artifactIndex, err := artifactIndexForPlatform(release, platform)
	if err != nil {
		return installItem{}, err
	}
	return newInstallItem(release, artifactIndex, platform, nil)
}

func legacyLocalPackageID(path, platform string) string {
	name := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".tar.gz"), ".tgz")
	parts := strings.Split(name, "_"+platform+"_")
	if len(parts) >= 2 {
		return parts[0]
	}
	return name
}

func artifactIndexForPlatform(release resolution.ResolvedRelease, platform string) (int, error) {
	target, err := resolution.TargetFromPlatformTuple(platform)
	if err != nil {
		return 0, fmt.Errorf("invalid current platform %q: %w", platform, err)
	}
	selected := -1
	for i := range release.Artifacts {
		if release.Artifacts[i].Target == target {
			if selected >= 0 {
				return 0, fmt.Errorf("resolved release for %s has multiple artifacts for platform %s", release.DriverID, platform)
			}
			selected = i
		}
	}
	if selected < 0 {
		return 0, fmt.Errorf("resolved release for %s has no artifact for platform %s", release.DriverID, platform)
	}
	return selected, nil
}

func (m progressiveInstallModel) newDirectInstallExecutor(item installItem) (*packageExecutor, error) {
	baseDir := ""
	if item.Release.Source.Type == "path" {
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve current directory for local package: %w", err)
		}
	}
	downloadArtifact := m.downloadArtifact
	if downloadArtifact != nil {
		underlying := downloadArtifact
		downloadArtifact = func(ctx context.Context, pkg dbc.PkgInfo) (io.ReadCloser, error) {
			body, err := underlying(ctx, pkg)
			if err != nil {
				return nil, err
			}
			var total int64
			if pkg.ArtifactSize != nil {
				total = *pkg.ArtifactSize
			}
			return &installProgressReadCloser{ReadCloser: body, total: total}, nil
		}
	}
	return newPackageExecutor(m.cfg, baseDir, m.NoVerify, downloadArtifact, m.downloadPkg, m.fetchPackslipArtifact, nil), nil
}

type installProgressReadCloser struct {
	io.ReadCloser
	written int64
	total   int64
}

func (reader *installProgressReadCloser) Read(p []byte) (int, error) {
	n, err := reader.ReadCloser.Read(p)
	reader.written += int64(n)
	if n > 0 && prog != nil {
		prog.Send(progressMsg{written: reader.written, total: reader.total})
	}
	return n, err
}

func closeDirectInstallArchive(item *installItem) error {
	if item == nil {
		return nil
	}
	var err error
	if item.Archive != nil {
		err = errors.Join(err, item.Archive.Close())
		item.Archive = nil
	}
	if item.Validation != nil && item.Validation.Prepared != nil {
		err = errors.Join(err, item.Validation.Prepared.Close())
		item.Validation.Prepared = nil
	}
	return err
}

func installConfigForEnsure(cfg config.Config) (config.Config, error) {
	primary, err := config.EnsureLocation(cfg)
	if err != nil {
		return cfg, err
	}
	if cfg.Level != config.ConfigEnv {
		cfg.Location = primary
		return cfg, nil
	}
	roots := filepath.SplitList(cfg.Location)
	available := []string{primary}
	for _, root := range roots[1:] {
		info, statErr := os.Stat(root)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return cfg, fmt.Errorf("failed to inspect configured driver root %s: %w", root, statErr)
		}
		if !info.IsDir() {
			return cfg, fmt.Errorf("configured driver root %s is not a directory", root)
		}
		available = append(available, root)
	}
	cfg.Location = strings.Join(available, string(filepath.ListSeparator))
	return cfg, nil
}
