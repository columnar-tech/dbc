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
	"net/url"
	"testing"

	"github.com/columnar-tech/dbc/auth"
)

func TestWithCredentialResolver(t *testing.T) {
	want := &auth.Credential{}
	var gotHost string
	c, err := NewClient(WithCredentialResolver(func(u *url.URL) (*auth.Credential, error) {
		gotHost = u.Host
		return want, nil
	}))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	got, err := c.credentialResolver(&url.URL{Host: "registry.example.com"})
	if err != nil {
		t.Fatalf("resolver returned error: %v", err)
	}
	if gotHost != "registry.example.com" {
		t.Fatalf("resolver host = %q, want registry.example.com", gotHost)
	}
	if got != want {
		t.Fatal("resolver returned a different credential than provided")
	}
}

func TestWithCredentialResolverDefault(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.credentialResolver == nil {
		t.Fatal("default credentialResolver should be non-nil")
	}
}
