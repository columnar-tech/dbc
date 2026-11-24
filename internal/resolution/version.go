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

package resolution

import "github.com/Masterminds/semver/v3"

// VersionEligible reports whether v may be selected without a pin. A
// constraint alone decides, including whether a prerelease is allowed;
// without one, a prerelease needs allowPrerelease.
func VersionEligible(v *semver.Version, constraint *semver.Constraints, allowPrerelease bool) bool {
	if constraint != nil {
		return constraint.Check(v)
	}
	return allowPrerelease || v.Prerelease() == ""
}
