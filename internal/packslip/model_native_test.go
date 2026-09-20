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
	"os"
	"testing"

	"github.com/columnar-tech/dbc/internal/packslipverify"
	"github.com/stretchr/testify/require"
)

func TestRealSigstoreFixtureIsVerifiedBeforePackslipPredicateIsRejected(t *testing.T) {
	trustedRootJSON, err := os.ReadFile("../packslipverify/testdata/trusted-root-public-good.json")
	require.NoError(t, err)
	bundleJSON, err := os.ReadFile("../packslipverify/testdata/bundle-provenance.json")
	require.NoError(t, err)
	verifier, err := packslipverify.New(packslipverify.Config{TrustedRootJSON: trustedRootJSON})
	require.NoError(t, err)
	policy, err := githubIdentityPolicy("github.com/sigstore/sigstore-js")
	require.NoError(t, err)
	resolver := &nativeResolver{verifier: verifier}
	verified, err := resolver.verifyBundle(context.Background(), bundleJSON, policy)
	require.NoError(t, err)
	_, err = parseRelease(verified.payload, "github.com/sigstore/sigstore-js")
	require.ErrorContains(t, err, "_type")
}
