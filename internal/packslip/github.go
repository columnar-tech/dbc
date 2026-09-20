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

//go:build !js

package packslip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultGitHubAPIBase = "https://api.github.com"
	defaultGitHubRawBase = "https://raw.githubusercontent.com"
	maxGitHubPageCount   = 100
	maxMetadataBytes     = 32 << 20
)

var errHTTPNotFound = errors.New("HTTP resource not found")

// GitHubPackslipDiscovery discovers release assets and the optional
// supplementary signed release list for a GitHub project.
type GitHubPackslipDiscovery struct {
	client  *http.Client
	apiBase *url.URL
	rawBase *url.URL
}

// NewGitHubPackslipDiscovery creates a discovery transport. Empty endpoint
// URLs select the public GitHub API and raw content host.
func NewGitHubPackslipDiscovery(client *http.Client, apiBaseURL, rawBaseURL string) (*GitHubPackslipDiscovery, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if apiBaseURL == "" {
		apiBaseURL = defaultGitHubAPIBase
	}
	if rawBaseURL == "" {
		rawBaseURL = defaultGitHubRawBase
	}
	apiBase, err := parseBaseURL(apiBaseURL)
	if err != nil {
		return nil, fmt.Errorf("GitHub API base URL: %w", err)
	}
	rawBase, err := parseBaseURL(rawBaseURL)
	if err != nil {
		return nil, fmt.Errorf("GitHub raw-content base URL: %w", err)
	}
	return &GitHubPackslipDiscovery{client: client, apiBase: apiBase, rawBase: rawBase}, nil
}

func parseBaseURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("expected an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("non-HTTPS endpoint is allowed only for loopback tests")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed, nil
}

func (d *GitHubPackslipDiscovery) ListReleases(ctx context.Context, source PackslipSource) ([]githubRelease, error) {
	project, err := normalizeProject(source.Project)
	if err != nil {
		return nil, err
	}
	owner, repo, _ := projectParts(project)
	result := make([]githubRelease, 0)
	seenTags := map[string]bool{}
	for page := 1; page <= maxGitHubPageCount; page++ {
		endpoint := *d.apiBase
		endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases"
		query := endpoint.Query()
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		endpoint.RawQuery = query.Encode()
		body, _, err := d.get(ctx, endpoint.String(), true)
		if err != nil {
			return nil, fmt.Errorf("list GitHub releases for %s: %w", project, err)
		}
		var releases []githubRelease
		if err := json.Unmarshal(body, &releases); err != nil {
			return nil, fmt.Errorf("decode GitHub releases for %s: %w", project, err)
		}
		if len(releases) == 0 {
			return result, nil
		}
		for _, release := range releases {
			if release.Draft {
				continue
			}
			if release.TagName == "" {
				return nil, fmt.Errorf("GitHub release for %s has an empty tag", project)
			}
			if seenTags[release.TagName] {
				return nil, fmt.Errorf("GitHub returned duplicate release tag %q", release.TagName)
			}
			seenTags[release.TagName] = true
			result = append(result, release)
		}
		if len(releases) < 100 {
			return result, nil
		}
	}
	return nil, fmt.Errorf("GitHub release listing exceeded %d pages", maxGitHubPageCount)
}

func (d *GitHubPackslipDiscovery) ReleaseListURL(source PackslipSource) (string, error) {
	project, err := normalizeProject(source.Project)
	if err != nil {
		return "", err
	}
	owner, repo, tool := projectParts(project)
	u := *d.rawBase
	path := []string{owner, repo, "HEAD", ".well-known"}
	if len(tool) == 0 {
		path = append(path, "packslip.json")
	} else {
		path = append(path, "packslip")
		path = append(path, tool[:len(tool)-1]...)
		path = append(path, tool[len(tool)-1]+".json")
	}
	encoded := make([]string, len(path))
	for i, part := range path {
		encoded[i] = url.PathEscape(part)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + strings.Join(encoded, "/")
	return u.String(), nil
}

func (d *GitHubPackslipDiscovery) get(ctx context.Context, resourceURL string, githubAPI bool) ([]byte, string, error) {
	initialURL, err := url.Parse(resourceURL)
	if err != nil {
		return nil, "", err
	}
	base := d.rawBase
	if githubAPI {
		base = d.apiBase
	}
	var loopbackHTTPEndpoint *url.URL
	if strings.EqualFold(base.Scheme, "http") && isLoopbackHost(base.Hostname()) && sameAuthority(initialURL, base) {
		loopbackHTTPEndpoint = initialURL
	}
	if err := validateMetadataURL(initialURL, loopbackHTTPEndpoint); err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", "dbc-packslip/0.4.0")
	if githubAPI {
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	client := *d.client
	checkRedirect := d.client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := validateRedirectChain(next, via, loopbackHTTPEndpoint); err != nil {
			return err
		}
		if checkRedirect != nil {
			if err := checkRedirect(next, via); err != nil {
				return err
			}
			// Caller policies may adjust the next request, so validate the full
			// chain again before net/http follows it.
			return validateRedirectChain(next, via, loopbackHTTPEndpoint)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 metadata redirects")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	finalURL := resourceURL
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
		if err := validateMetadataURL(response.Request.URL, loopbackHTTPEndpoint); err != nil {
			return nil, finalURL, err
		}
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, finalURL, errHTTPNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, finalURL, fmt.Errorf("HTTP %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMetadataBytes+1))
	if err != nil {
		return nil, finalURL, err
	}
	if len(body) > maxMetadataBytes {
		return nil, finalURL, fmt.Errorf("metadata response exceeds %d bytes", maxMetadataBytes)
	}
	return body, finalURL, nil
}

func validateRedirectChain(next *http.Request, via []*http.Request, loopbackHTTPEndpoint *url.URL) error {
	if next == nil || next.URL == nil || len(via) == 0 {
		return errors.New("metadata redirect has no valid request chain")
	}
	var previousURL *url.URL
	for _, previous := range via {
		if previous == nil || previous.URL == nil {
			return errors.New("metadata redirect has an invalid previous request")
		}
		if err := validateMetadataURL(previous.URL, loopbackHTTPEndpoint); err != nil {
			return fmt.Errorf("metadata redirect chain rejected: %w", err)
		}
		if previousURL != nil && strings.EqualFold(previousURL.Scheme, "https") && strings.EqualFold(previous.URL.Scheme, "http") {
			return errors.New("metadata redirect chain attempted an HTTPS-to-HTTP downgrade")
		}
		previousURL = previous.URL
	}
	initialURL := via[0].URL
	if strings.EqualFold(initialURL.Scheme, "http") && loopbackHTTPEndpoint == nil {
		return errors.New("HTTP metadata is allowed only for a configured loopback test endpoint")
	}
	if strings.EqualFold(previousURL.Scheme, "https") && strings.EqualFold(next.URL.Scheme, "http") {
		return errors.New("metadata redirect attempted an HTTPS-to-HTTP downgrade")
	}
	if err := validateMetadataURL(next.URL, loopbackHTTPEndpoint); err != nil {
		return fmt.Errorf("metadata redirect rejected: %w", err)
	}
	return nil
}

func validateMetadataURL(target *url.URL, loopbackHTTPEndpoint *url.URL) error {
	if target == nil || target.Hostname() == "" || target.User != nil || target.Fragment != "" {
		return errors.New("metadata URL must have a host and no credentials or fragment")
	}
	switch strings.ToLower(target.Scheme) {
	case "https":
		return nil
	case "http":
		if loopbackHTTPEndpoint != nil && isLoopbackHost(target.Hostname()) && sameAuthority(target, loopbackHTTPEndpoint) {
			return nil
		}
		return errors.New("HTTP metadata is allowed only for a configured loopback test endpoint")
	default:
		return fmt.Errorf("metadata URL scheme %q is not HTTPS", target.Scheme)
	}
}

func sameAuthority(left, right *url.URL) bool {
	return left != nil && right != nil && strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Host, right.Host)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
