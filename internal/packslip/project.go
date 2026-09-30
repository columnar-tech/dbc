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

import "strings"

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
