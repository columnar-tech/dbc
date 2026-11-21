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
	"slices"
	"strings"
)

// urlPrefixLen returns the length of an "http://" or "https://" prefix of s
// (ignoring case), or 0.
func urlPrefixLen(s string) int {
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			return len(prefix)
		}
	}
	return 0
}

// diagnosticURLEnd returns where the URL that starts at s[start:] (after its
// scheme) ends. A quote just before the URL makes the matching quote the end.
// Once a query or fragment starts, everything is part of the URL: up to the
// closing quote, or otherwise up to the next whitespace, so that quotes and
// brackets inside a secret cannot end it early. Punctuation that closes the
// sentence is left outside.
func diagnosticURLEnd(s string, start int, quote byte) int {
	const trailing = ".,:;)]}'\""
	j := start
	for ; j < len(s); j++ {
		c := s[j]
		if c == '?' || c == '#' {
			queryStart := j
			if quote != 0 {
				for ; j < len(s) && (s[j] != quote || s[j-1] == '\\'); j++ {
				}
				return j
			}
			for ; j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '\n' && s[j] != '\r'; j++ {
			}
			for j > queryStart && strings.IndexByte(trailing, s[j-1]) >= 0 {
				j--
			}
			return j
		}
		if quote != 0 {
			if c == quote {
				return j
			}
		} else if strings.IndexByte(" \t\n\r\"'()<>", c) >= 0 {
			break
		}
	}
	if quote == 0 {
		for j > start && strings.IndexByte(".,:;", s[j-1]) >= 0 {
			j--
		}
	}
	return j
}

// stripURLSecrets keeps only scheme, host and path of a URL string. It works
// on the text rather than on parsed values, so no encoding of the userinfo or
// the query can slip through.
func stripURLSecrets(raw string) string {
	schemeEnd := strings.Index(raw, "://") + len("://")
	scheme, rest := raw[:schemeEnd], raw[schemeEnd:]
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	authorityEnd := strings.Index(rest, "/")
	if authorityEnd < 0 {
		authorityEnd = len(rest)
	}
	if at := strings.LastIndex(rest[:authorityEnd], "@"); at >= 0 {
		rest = rest[at+1:]
	}
	return scheme + rest
}

// minRedactableSecretLen is the shortest secret replaced in place; a text
// containing a shorter one is hidden entirely.
const minRedactableSecretLen = 4

// HiddenMessage replaces a text that Redact cannot clean reliably.
const HiddenMessage = "registry request failed; details are hidden because they may contain credentials"

// Secrets lists the values in registry URLs that must never be shown:
// userinfo and query values, in raw and escaped spellings, longest first.
func Secrets(urls []*url.URL) []string {
	seen := map[string]bool{}
	var secrets []string
	add := func(v string) {
		for _, form := range []string{v, url.QueryEscape(v), url.PathEscape(v)} {
			if form != "" && !seen[form] {
				seen[form] = true
				secrets = append(secrets, form)
			}
		}
	}
	for _, u := range urls {
		if u == nil {
			continue
		}
		if u := u.User; u != nil {
			add(u.Username())
			if password, ok := u.Password(); ok {
				add(password)
			}
		}
		for _, values := range u.Query() {
			for _, v := range values {
				add(v)
			}
		}
		for pair := range strings.SplitSeq(u.RawQuery, "&") {
			if _, v, ok := strings.Cut(pair, "="); ok {
				add(v)
			}
		}
	}
	// Longer secrets first, so one that contains another is replaced whole.
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	return secrets
}

// Redact removes credentials from text in two layers. Every URL loses its
// userinfo, query and fragment, whatever their encoding. Then every value in
// secrets is replaced by "REDACTED", for text that is not part of a URL. A
// text that contains a secret too short to replace safely becomes
// HiddenMessage.
func Redact(text string, secrets []string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		n := urlPrefixLen(text[i:])
		if n == 0 {
			b.WriteByte(text[i])
			i++
			continue
		}
		var quote byte
		if i > 0 && (text[i-1] == '"' || text[i-1] == '\'') {
			quote = text[i-1]
		}
		end := diagnosticURLEnd(text, i+n, quote)
		b.WriteString(stripURLSecrets(text[i:end]))
		i = end
	}
	msg := b.String()
	for _, secret := range secrets {
		if len(secret) < minRedactableSecretLen {
			// Replacing a short value inside arbitrary text could mangle
			// the message without reliably hiding the secret, so hide the
			// whole diagnostic instead.
			if strings.Contains(msg, secret) {
				return HiddenMessage
			}
			continue
		}
		msg = strings.ReplaceAll(msg, secret, "REDACTED")
	}
	return msg
}
