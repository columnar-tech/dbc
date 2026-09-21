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
	"errors"
	"fmt"
	"strings"
)

// DriverSourceType identifies the source declared for a driver in dbc.toml.
type DriverSourceType string

const (
	DriverSourceRegistry DriverSourceType = "registry"
	DriverSourcePackslip DriverSourceType = "packslip"
	DriverSourcePath     DriverSourceType = "path"
)

// DriverSource is the source identity declared for one project driver.
// Source identity is kept separate from runtime DriverInfo.Source, which marks
// who owns the installed registration.
type DriverSource struct {
	Type    DriverSourceType `toml:"type" json:"type"`
	URL     string           `toml:"url,omitempty" json:"url,omitempty"`
	Project string           `toml:"project,omitempty" json:"project,omitempty"`
	Path    string           `toml:"path,omitempty" json:"path,omitempty"`
}

// Validate checks that the source's type and source-specific field agree.
// Registry URL and path resolution remain the responsibility of their
// adapters; Packslip projects use the currently supported GitHub host path.
func (s DriverSource) Validate() error {
	switch s.Type {
	case DriverSourceRegistry:
		if s.URL == "" {
			return errors.New("registry source has no URL")
		}
		if s.Project != "" || s.Path != "" {
			return errors.New("registry source contains fields for another source type")
		}
	case DriverSourcePackslip:
		if s.Project == "" {
			return errors.New("packslip source has no project")
		}
		if err := validatePackslipProject(s.Project); err != nil {
			return err
		}
		if s.URL != "" || s.Path != "" {
			return errors.New("packslip source contains fields for another source type")
		}
	case DriverSourcePath:
		if s.Path == "" {
			return errors.New("path source has no path")
		}
		if s.URL != "" || s.Project != "" {
			return errors.New("path source contains fields for another source type")
		}
	default:
		if s.Type == "" {
			return errors.New("driver source has no type")
		}
		return fmt.Errorf("unsupported driver source type %q", s.Type)
	}
	return nil
}

func validatePackslipProject(project string) error {
	if project != strings.TrimSpace(project) || strings.Contains(project, "://") || strings.HasSuffix(project, "/") {
		return fmt.Errorf("packslip project must be a GitHub host path without a URL scheme: %q", project)
	}
	parts := strings.Split(project, "/")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "github.com") {
		return fmt.Errorf("packslip project must use github.com/owner/repo[/tool...]: %q", project)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("packslip project contains an invalid path segment: %q", project)
		}
		for _, ch := range part {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("._-", ch)) {
				return fmt.Errorf("packslip project contains an invalid path segment: %q", project)
			}
		}
	}
	return nil
}
