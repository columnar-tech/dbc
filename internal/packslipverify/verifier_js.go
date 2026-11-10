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

import "context"

// unsupportedVerifier is the Wasm stub. Packslip resolution and replay are
// unsupported on Wasm, and this build must not link sigstore-go.
type unsupportedVerifier struct{}

// New returns a Verifier whose VerifyKeyless always fails with
// [ErrUnsupported]. The Config is ignored.
func New(Config) (Verifier, error) { return unsupportedVerifier{}, nil }

func (unsupportedVerifier) VerifyKeyless(context.Context, []byte, string) (*Verified, error) {
	return nil, ErrUnsupported
}
