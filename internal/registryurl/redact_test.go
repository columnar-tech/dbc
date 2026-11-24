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

package registryurl

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecrets(t *testing.T) {
	parse := func(s string) *url.URL {
		t.Helper()
		u, err := url.Parse(s)
		require.NoError(t, err)
		return u
	}
	tests := []struct {
		name string
		urls []*url.URL
		want []string
	}{
		{"none", []*url.URL{nil, parse("https://host/base")}, nil},
		{"userinfo", []*url.URL{parse("https://user:pass@host")}, []string{"user", "pass"}},
		{"username only", []*url.URL{parse("https://token@host")}, []string{"token"}},
		// The decoded and the raw spelling, each also query- and path-escaped.
		{"escaped query value", []*url.URL{parse("https://host?t=a%2Fb")}, []string{"a/b", "a%2Fb", "a%252Fb"}},
		{"space in query value", []*url.URL{parse("https://host?t=a+b")}, []string{"a b", "a+b", "a%20b", "a%2Bb"}},
		{"longest first", []*url.URL{parse("https://ab:abcdef@host?k=abc")}, []string{"abcdef", "abc", "ab"}},
		{"deduplicated", []*url.URL{parse("https://same@host?k=same"), parse("https://same@other")}, []string{"same"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Secrets(tt.urls)
			assert.ElementsMatch(t, tt.want, got)
			// Longer secrets come first, so one containing another is
			// replaced whole.
			assert.IsNonIncreasing(t, lengths(got))
		})
	}
}

func lengths(ss []string) []int {
	out := make([]int, len(ss))
	for i, s := range ss {
		out[i] = len(s)
	}
	return out
}

func TestRedact(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		secrets []string
		want    string
	}{
		{"no URL", "connection refused", nil, "connection refused"},
		{"userinfo", "Get https://u:p@host/index.yaml: EOF", nil, "Get https://host/index.yaml: EOF"},
		{"query and fragment", "https://host/p?token=SECRET#frag failed", nil, "https://host/p failed"},
		{"scheme case", "HTTP://u:p@host/p?k=v.", nil, "HTTP://host/p."},
		{"quoted URL with spaces", `Get "https://host/p?t=a b": EOF`, nil, `Get "https://host/p": EOF`},
		{"trailing punctuation", "see (https://host/p?t=SECRET).", nil, "see (https://host/p)."},
		{"secret outside a URL", "user SECRETUSER rejected", []string{"SECRETUSER"}, "user REDACTED rejected"},
		{"longer secret first", "token SECRETLONG", []string{"SECRETLONG", "SECRET"}, "token REDACTED"},
		{"short secret present", "token q7 rejected", []string{"q7"}, HiddenMessage},
		{"short secret absent", "no secret here", []string{"q7"}, "no secret here"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Redact(tt.in, tt.secrets))
		})
	}
}
