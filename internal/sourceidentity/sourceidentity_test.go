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

package sourceidentity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryIdentity(t *testing.T) {
	tests := []struct {
		name  string
		a     string
		b     string
		equal bool
	}{
		{name: "scheme and host case normalize", a: "HTTPS://R.Example.test/a", b: "https://r.example.test/a", equal: true},
		{name: "literal trailing slash normalizes", a: "https://r.example.test/a///", b: "https://r.example.test/a", equal: true},
		{name: "fragment is excluded", a: "https://r.example.test/a#one", b: "https://r.example.test/a#two", equal: true},
		{name: "query is preserved", a: "https://r.example.test/?tenant=a", b: "https://r.example.test/?tenant=b"},
		{name: "query order is preserved", a: "https://r.example.test/?a=1&b=2", b: "https://r.example.test/?b=2&a=1"},
		{name: "userinfo is preserved", a: "https://user-a:pw@r.example.test", b: "https://user-b:pw@r.example.test"},
		{name: "empty force query is preserved", a: "https://r.example.test?", b: "https://r.example.test"},
		{name: "escaped separator differs from literal separator", a: "https://r.example.test/a%2Fb", b: "https://r.example.test/a/b"},
		{name: "escaped trailing separator is not a literal trailing slash", a: "https://r.example.test/a%2F", b: "https://r.example.test/a"},
		{name: "path segments are not cleaned", a: "https://r.example.test/a/../b", b: "https://r.example.test/b"},
		{name: "default port is preserved", a: "https://r.example.test:443", b: "https://r.example.test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a, err := Parse(Registry, test.a)
			require.NoError(t, err)
			b, err := Parse(Registry, test.b)
			require.NoError(t, err)
			assert.Equal(t, test.equal, a == b)
		})
	}
}

func TestParseValidatesEachSourceKind(t *testing.T) {
	tests := []struct {
		name      string
		kind      Kind
		reference string
		wantKey   Key
		wantErr   string
	}{
		{name: "registry", kind: Registry, reference: "https://registry.example.test/", wantKey: Key{Kind: Registry, Reference: "https://registry.example.test"}},
		{name: "registry requires HTTP(S)", kind: Registry, reference: "ftp://registry.example.test", wantErr: "scheme must be http or https"},
		{name: "registry requires a host", kind: Registry, reference: "https:///registry", wantErr: "missing host"},
		{name: "packslip host owner repo case folds", kind: Packslip, reference: "GitHub.COM/Acme/Driver/Tools/Tool", wantKey: Key{Kind: Packslip, Reference: "github.com/acme/driver/Tools/Tool"}},
		{name: "packslip rejects a URL", kind: Packslip, reference: "https://github.com/acme/driver", wantErr: "without a URL scheme"},
		{name: "packslip rejects malformed segment", kind: Packslip, reference: "github.com/acme//driver", wantErr: "invalid path segment"},
		{name: "path preserves exact string", kind: Path, reference: "../packages/./driver.tgz", wantKey: Key{Kind: Path, Reference: "../packages/./driver.tgz"}},
		{name: "path requires a value", kind: Path, wantErr: "path source has no path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, err := Parse(test.kind, test.reference)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantKey, key)
		})
	}
}
