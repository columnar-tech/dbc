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

// Package registryurl validates registry URLs. url.Parse alone accepts
// strings with no scheme or host, which are useless as a registry.
package registryurl

import (
	"errors"
	"fmt"
	"net/url"
)

// Parse parses s and requires an http or https scheme and a non-empty host.
func Parse(s string) (*url.URL, error) {
	if s == "" {
		return nil, errors.New("empty url")
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", s, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%q: missing host", s)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%q: scheme must be http or https", s)
	}
	return u, nil
}
