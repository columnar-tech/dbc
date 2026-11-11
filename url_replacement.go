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
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// URLReplacement sends requests for URLs under Prefix to Replacement instead.
//
// Prefix and Replacement are absolute http or https URLs without userinfo,
// query or fragment. A request URL matches when its origin (scheme, host and
// port) equals the prefix's and the prefix path is a prefix of the request
// path at a path segment boundary. Percent-encoded unreserved ASCII
// characters compare as their literal characters; reserved escapes such as
// %2F stay escaped and do not become path separators. The matching part of
// the URL is then replaced by Replacement. Requests that were replaced are always anonymous:
// no credentials, no telemetry identifiers and no fallback to the original
// URL.
type URLReplacement struct {
	Prefix      string `toml:"prefix"`
	Replacement string `toml:"replacement"`
}

// NetworkConfig holds network settings of the user's config.toml.
type NetworkConfig struct {
	// URLReplacements is an ordered list of rules; the first matching rule
	// wins and its result is never matched again.
	URLReplacements []URLReplacement `toml:"url_replacements,omitempty"`
}

// ErrInvalidURLReplacement is wrapped by the errors returned for invalid URL
// replacement rules.
var ErrInvalidURLReplacement = errors.New("invalid URL replacement")

// urlRule is a validated URLReplacement.
type urlRule struct {
	origin redirectOrigin
	// path is the escaped prefix path with unreserved escapes normalized and
	// without a trailing slash.
	path string
	// scheme, host and replPath describe the replacement; replPath is the
	// escaped path without a trailing slash.
	scheme   string
	host     string
	replPath string
}

// urlRouter applies URL replacement rules. It performs no I/O.
type urlRouter struct {
	rules []urlRule
}

func parseReplacementURL(raw string) (*url.URL, error) {
	if strings.ContainsAny(raw, "?#") {
		return nil, errors.New("must not contain a query or fragment")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("must be an absolute http or https URL")
	}
	if u.Hostname() == "" {
		return nil, errors.New("must include a host")
	}
	if u.User != nil {
		return nil, errors.New("must not contain userinfo")
	}
	return u, nil
}

// newURLRouter validates the rules as a whole and returns a router. It returns
// a nil router when there are no rules.
func newURLRouter(rules []URLReplacement) (*urlRouter, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	r := &urlRouter{rules: make([]urlRule, 0, len(rules))}
	for i, rule := range rules {
		p, err := parseReplacementURL(rule.Prefix)
		if err != nil {
			return nil, fmt.Errorf("%w: network.url_replacements[%d]: invalid prefix %q: %w", ErrInvalidURLReplacement, i, rule.Prefix, err)
		}
		repl, err := parseReplacementURL(rule.Replacement)
		if err != nil {
			return nil, fmt.Errorf("%w: network.url_replacements[%d]: invalid replacement %q: %w", ErrInvalidURLReplacement, i, rule.Replacement, err)
		}
		if strings.EqualFold(p.Scheme, "https") && strings.EqualFold(repl.Scheme, "http") {
			return nil, fmt.Errorf("%w: network.url_replacements[%d]: an https prefix must not be replaced by an http URL", ErrInvalidURLReplacement, i)
		}
		r.rules = append(r.rules, urlRule{
			origin:   originOf(p),
			path:     strings.TrimRight(normalizeEscapedPath(p.EscapedPath()).path, "/"),
			scheme:   strings.ToLower(repl.Scheme),
			host:     repl.Host,
			replPath: strings.TrimRight(repl.EscapedPath(), "/"),
		})
	}
	return r, nil
}

// route returns the effective URL for u. replaced is false when no rule
// matches (or r is nil); the first matching rule wins and its result is not
// matched again. A matching URL that carries a query string is an error,
// because queries are never sent to a replacement.
func (r *urlRouter) route(u *url.URL) (eff *url.URL, replaced bool, err error) {
	if r == nil {
		return nil, false, nil
	}
	o := originOf(u)
	escapedPath := u.EscapedPath()
	normalized := normalizeEscapedPath(escapedPath)
	p := normalized.path
	for _, rule := range r.rules {
		if rule.origin != o {
			continue
		}
		if p != rule.path && !strings.HasPrefix(p, rule.path+"/") {
			continue
		}
		if u.RawQuery != "" || u.ForceQuery {
			return nil, false, fmt.Errorf("refusing to send %s%s to a URL replacement: the URL has a query string, which is never sent to a replacement", u.Host, p)
		}
		suffixOffset := normalized.sourceOffset(len(rule.path))
		eff, err = url.Parse(rule.scheme + "://" + rule.host + rule.replPath + escapedPath[suffixOffset:])
		if err != nil {
			return nil, false, fmt.Errorf("failed to build replacement URL for %s%s: %w", u.Host, p, err)
		}
		return eff, true, nil
	}
	return nil, false, nil
}

// anonymousGet performs a GET for a replaced request. It sends no credentials
// and no query, never falls back to the original URL, and reports every
// failure (including non-200 statuses) naming the effective host and path.
// canonicalPath decides the Accept header, as for unreplaced requests. A
// redirect that leads back to a URL some rule replaces is refused, so a mirror
// cannot send the request to the site it stands in for.
func anonymousGet(ctx context.Context, hc *http.Client, r *urlRouter, eff *url.URL, canonicalPath string) (*http.Response, error) {
	where := eff.Host + eff.EscapedPath()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eff.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request for %s: %w", where, err)
	}
	if canonicalPath == "/index.yaml" {
		req.Header.Set("Accept", "application/yaml")
	}
	resp, err := r.withoutRedirectsToReplaced(hc).Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s (URL replacement): %w", where, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %w", where, ErrUnauthorized)
	}
	resp.Body.Close()
	return nil, fmt.Errorf("request to %s (URL replacement): %s", where, resp.Status)
}

// withoutRedirectsToReplaced returns a copy of hc that keeps hc's redirect
// policy and additionally refuses redirects to URLs that a rule replaces.
func (r *urlRouter) withoutRedirectsToReplaced(hc *http.Client) *http.Client {
	guarded := *hc
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && strings.EqualFold(via[len(via)-1].URL.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http") {
			return errors.New("refusing HTTPS to HTTP redirect for URL replacement request")
		}
		if _, replaced, err := r.route(req.URL); replaced || err != nil {
			return fmt.Errorf("refusing redirect to %s, which a URL replacement rule replaces", req.URL.Host)
		}
		if hc.CheckRedirect != nil {
			return hc.CheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &guarded
}

type normalizedEscapedPath struct {
	path    string
	offsets []int
}

// normalizeEscapedPath decodes percent-encoded unreserved ASCII characters
// and uppercases retained escapes. offsets maps normalized byte boundaries
// back to the corresponding boundary in the original escaped path.
func normalizeEscapedPath(path string) normalizedEscapedPath {
	var normalized strings.Builder
	normalized.Grow(len(path))
	offsets := make([]int, 1, len(path)+1)
	offsets[0] = 0
	for i := 0; i < len(path); {
		start := i
		var token string
		if path[i] == '%' && i+2 < len(path) {
			if high, ok := fromHex(path[i+1]); ok {
				if low, ok := fromHex(path[i+2]); ok {
					value := high<<4 | low
					if isUnreserved(value) {
						token = string([]byte{value})
					} else {
						token = strings.ToUpper(path[i : i+3])
					}
					i += 3
				}
			}
		}
		if token == "" {
			token = path[i : i+1]
			i++
		}
		normalized.WriteString(token)
		for j := 1; j <= len(token); j++ {
			if j == len(token) {
				offsets = append(offsets, i)
			} else {
				offsets = append(offsets, start)
			}
		}
	}
	return normalizedEscapedPath{path: normalized.String(), offsets: offsets}
}

func (p normalizedEscapedPath) sourceOffset(normalizedOffset int) int {
	if normalizedOffset <= 0 {
		return 0
	}
	if normalizedOffset >= len(p.offsets) {
		return p.offsets[len(p.offsets)-1]
	}
	return p.offsets[normalizedOffset]
}

func fromHex(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}

func isUnreserved(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '.' || b == '_' || b == '~'
}

var defaultRouter atomic.Pointer[urlRouter]

// SetDefaultURLReplacements sets the URL replacement rules used by the
// deprecated package-level functions (GetDriverList and
// PkgInfo.DownloadPackage). Passing no rules clears them. It returns an error,
// leaving the previous rules in place, if a rule is invalid.
func SetDefaultURLReplacements(rules []URLReplacement) error {
	r, err := newURLRouter(rules)
	if err != nil {
		return err
	}
	defaultRouter.Store(r)
	return nil
}

// WithURLReplacements sets the URL replacement rules for the client. They
// override the rules of a global config passed to WithGlobalConfig. They
// apply independently of WithBaseURL and WithRegistries.
func WithURLReplacements(rules []URLReplacement) Option {
	rulesCopy := append([]URLReplacement(nil), rules...)
	return func(c *clientConfig) {
		c.urlReplacements = rulesCopy
		c.explicitURLReplacements = true
	}
}
