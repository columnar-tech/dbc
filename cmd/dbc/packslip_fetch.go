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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	packslipArtifactRedirectLimit = 10
	packslipErrorBodyLimit        = 4096
)

var packslipArtifactClient = newPackslipArtifactHTTPClient()

func newPackslipArtifactHTTPClient() *http.Client {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= packslipArtifactRedirectLimit {
				return fmt.Errorf("Packslip artifact redirect limit (%d) exceeded", packslipArtifactRedirectLimit)
			}
			if err := validatePackslipArtifactURL(request.URL); err != nil {
				return fmt.Errorf("reject Packslip artifact redirect: %w", err)
			}
			return nil
		},
	}
}

func fetchPackslipArtifact(ctx context.Context, artifactURL *url.URL) (io.ReadCloser, error) {
	return fetchPackslipArtifactWithClient(ctx, packslipArtifactClient, artifactURL)
}

// fetchPackslipArtifactWithClient keeps tests transport-local while ensuring
// that the security policy is applied even when the supplied client has a
// different redirect callback or a cookie jar.
func fetchPackslipArtifactWithClient(ctx context.Context, client *http.Client, artifactURL *url.URL) (io.ReadCloser, error) {
	if ctx == nil {
		return nil, errors.New("Packslip artifact fetch requires a context")
	}
	if err := validatePackslipArtifactURL(artifactURL); err != nil {
		return nil, err
	}
	if client == nil {
		client = packslipArtifactClient
	}
	copy := *client
	copy.Jar = nil
	previousRedirectCheck := client.CheckRedirect
	copy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= packslipArtifactRedirectLimit {
			return fmt.Errorf("Packslip artifact redirect limit (%d) exceeded", packslipArtifactRedirectLimit)
		}
		if err := validatePackslipArtifactURL(request.URL); err != nil {
			return fmt.Errorf("reject Packslip artifact redirect: %w", err)
		}
		if previousRedirectCheck != nil {
			return previousRedirectCheck(request, via)
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Packslip artifact request: %w", err)
	}
	request.Header.Set("User-Agent", "dbc")
	response, err := copy.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Packslip artifact: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, packslipErrorBodyLimit))
		_ = response.Body.Close()
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			return nil, fmt.Errorf("fetch Packslip artifact: unexpected HTTP status %s", response.Status)
		}
		return nil, fmt.Errorf("fetch Packslip artifact: unexpected HTTP status %s: %s", response.Status, detail)
	}
	return response.Body, nil
}

func validatePackslipArtifactURL(artifactURL *url.URL) error {
	if artifactURL == nil || !strings.EqualFold(artifactURL.Scheme, "https") ||
		artifactURL.Hostname() == "" || artifactURL.User != nil || artifactURL.Fragment != "" || strings.Contains(artifactURL.String(), "#") {
		return errors.New("Packslip artifact URL must be HTTPS with a host and without userinfo or fragment")
	}
	return nil
}
