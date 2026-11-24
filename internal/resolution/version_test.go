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

import (
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestVersionEligible(t *testing.T) {
	stable, pre := semver.MustParse("1.0.0"), semver.MustParse("2.0.0-beta.1")
	c := func(s string) *semver.Constraints {
		t.Helper()
		constraint, err := semver.NewConstraint(s)
		if err != nil {
			t.Fatal(err)
		}
		return constraint
	}
	for _, tc := range []struct {
		name            string
		v               *semver.Version
		constraint      *semver.Constraints
		allowPrerelease bool
		want            bool
	}{
		{"stable without constraint", stable, nil, false, true},
		{"prerelease without constraint", pre, nil, false, false},
		{"prerelease allowed", pre, nil, true, true},
		{"constraint decides", stable, c(">=2.0.0-0"), true, false},
		{"constraint admits a prerelease", pre, c(">=2.0.0-0"), false, true},
		{"constraint rejects a prerelease", pre, c(">=1.0.0"), true, false},
	} {
		if got := VersionEligible(tc.v, tc.constraint, tc.allowPrerelease); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
