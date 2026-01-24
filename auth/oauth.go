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

package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

var (
	defaultOauthURI      = "dbc-cdn-private.columnar.tech"
	defaultOauthClientID = "eSKuasirO0gUnGuNURPagErV3TSSFhEK"
	licenseURI           = "https://heimdall.columnar.tech/trial_license"
)

func init() {
	if isStaging, _ := strconv.ParseBool(os.Getenv("DBC_USE_STAGING")); isStaging {
		defaultOauthURI = "dbc-cdn-private-staging.columnar.tech"
		defaultOauthClientID = "XZaxtg7XjYSTLNzgrLbYNrPOZzRiRpvW"
		licenseURI = "https://dbc-cf-api-staging.columnar.workers.dev/trial_license"
	}
}

func DefaultOauthURI() string      { return defaultOauthURI }
func DefaultOauthClientID() string { return defaultOauthClientID }

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
	for _, p := range []string{"openid-configuration", "oauth-authorization-server"} {
		u, _ := issuer.Parse("/.well-known/" + p)

		err = fetch(u, &config)
		if err == nil {
			return
		}
	}

	return config, err
}

func refreshOauth(cred *Credential) error {
	cfg, err := GetOpenIDConfig((*url.URL)(&cred.AuthURI))
	if err != nil {
		return err
	}

	values := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {cred.ClientID},
		"refresh_token": {cred.RefreshToken},
	}

	payload := values.Encode()
	req, _ := http.NewRequest(http.MethodPost, cfg.TokenEndpoint.String(),
		strings.NewReader(payload))
	req.Header.Add("content-type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token endpoint returned status %s", resp.Status)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return err
	}

	cred.Token = tokenResp.AccessToken
	return nil
}
