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

//go:build js

package packslipverify

import (
	"context"
	"fmt"
)

type unsupportedVerifier struct{}

// New returns a verifier that reports ErrUnsupported when Verify is called.
func New(Config) (PackslipVerifier, error) {
	return unsupportedVerifier{}, nil
}

func (unsupportedVerifier) Verify(context.Context, []byte, IdentityPolicy, ArtifactDigest) (*VerifiedBundle, error) {
	return nil, fmt.Errorf("%w: Node and browser builds cannot verify Sigstore bundles", ErrUnsupported)
}

var _ PackslipVerifier = unsupportedVerifier{}
