// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build js

package packslip

import (
	"context"
	"fmt"
	"net/http"

	"github.com/columnar-tech/dbc/internal/resolution"
)

type unsupportedResolver struct{}

// NewResolver returns a resolver that rejects unsupported sources before
// reading trust state or making a network request.
func NewResolver(Config) (Resolver, error) { return unsupportedResolver{}, nil }

func (unsupportedResolver) Resolve(context.Context, PackslipSource, Request) (resolution.ResolvedRelease, error) {
	return resolution.ResolvedRelease{}, fmt.Errorf("%w: Node and browser builds cannot resolve GitHub packslip sources", ErrUnsupported)
}

// GitHubPackslipDiscovery is an unavailable transport on JavaScript targets.
type GitHubPackslipDiscovery struct{}

func NewGitHubPackslipDiscovery(*http.Client, string, string) (*GitHubPackslipDiscovery, error) {
	return nil, ErrUnsupported
}

func NewTrustStore() (*TrustStore, error) { return nil, ErrUnsupported }

func NewTrustStoreAt(string) (*TrustStore, error) { return nil, ErrUnsupported }

var _ Resolver = unsupportedResolver{}
