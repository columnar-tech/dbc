// Copyright 2025 Columnar Technologies Inc.
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

package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

const (
	DefaultOauthURI      = "db806wgnqvjsv.cloudfront.net"
	DefaultOauthClientID = "eSKuasirO0gUnGuNURPagErV3TSSFhEK"
	DefaultOauthAudience = "https://dbc-cdn-private.columnar.tech"
)

type OpenIDConfig struct {
	Issuer                            Uri      `json:"issuer"`
	AuthorizationEndpoint             Uri      `json:"authorization_endpoint"`
	TokenEndpoint                     Uri      `json:"token_endpoint"`
	DeviceAuthorizationEndpoint       Uri      `json:"device_authorization_endpoint"`
	UserinfoEndpoint                  Uri      `json:"userinfo_endpoint"`
	JwksURI                           Uri      `json:"jwks_uri"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	SubjectTypesSupported             []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported  []string `json:"id_token_signing_alg_values_supported"`
	ClaimsSupported                   []string `json:"claims_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	EndSessionEndpoint                Uri      `json:"end_session_endpoint"`
	RequestURIParameterSupported      bool     `json:"request_uri_parameter_supported"`
	RequestParameterSupported         bool     `json:"request_parameter_supported"`
}

func fetch[T any](u *url.URL, dest *T) error {
	resp, err := http.Get(u.String())
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch %s: %s", u.String(), resp.Status)
	}

	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(dest)
}

func GetOpenIDConfig(issuer *url.URL) (config OpenIDConfig, err error) {
	for _, p := range []string{"oauth-authorization-server", "openid-configuration"} {
		u, _ := issuer.Parse("/.well-known/" + p)

		err = fetch(u, &config)
		if err == nil {
			return
		}
	}

	return config, err
}
