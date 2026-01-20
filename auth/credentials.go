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
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pelletier/go-toml/v2"
)

type Type string

const (
	TypeApiKey Type = "apikey"
	TypeToken  Type = "oauth"
)

func (a *Type) UnmarshalText(text []byte) error {
	switch string(text) {
	case "apikey":
		*a = TypeApiKey
	case "oauth":
		*a = TypeToken
	default:
		return fmt.Errorf("invalid auth type: %s", string(text))
	}
	return nil
}

type Uri url.URL

func (u *Uri) String() string {
	return (*url.URL)(u).String()
}

func (u *Uri) MarshalText() ([]byte, error) {
	return (*url.URL)(u).MarshalBinary()
}

func (u *Uri) UnmarshalText(text []byte) error {
	return (*url.URL)(u).UnmarshalBinary(text)
}

type Credential struct {
	Type         Type   `toml:"type"`
	AuthURI      Uri    `toml:"auth_uri"`
	RegistryURL  Uri    `toml:"registry_url"`
	ApiKey       string `toml:"api_key,omitempty"`
	Token        string `toml:"token"`
	RefreshToken string `toml:"refresh_token,omitempty"`
	ClientID     string `toml:"client_id,omitempty"`
	Audience     string `toml:"audience,omitempty"`
}

func (t *Credential) Refresh() bool {
	switch t.Type {
	case TypeApiKey:
		rsp, err := http.DefaultClient.Do(&http.Request{
			Method: http.MethodGet,
			URL:    (*url.URL)(&t.AuthURI),
			Header: http.Header{
				"authorization": []string{"Bearer " + t.ApiKey},
			},
		})
		if err != nil || rsp.StatusCode != http.StatusOK {
			return false
		}
		defer rsp.Body.Close()

		var tokenResp struct {
			Token string `json:"access_token"`
		}
		if err := json.NewDecoder(rsp.Body).Decode(&tokenResp); err != nil {
			return false
		}

		t.Token = tokenResp.Token
		return true
	case TypeToken:
		if err := refreshOauth(t); err != nil {
			return false
		}
		return true
	}

	return false
}

func (t *Credential) GetAuthToken() string {
	if t.Token != "" {
		return t.Token
	}

	if t.Refresh() {
		_ = UpdateCreds()
		return t.Token
	}

	return ""
}

var (
	loadedCredentials []Credential
	credentialErr     error
	loaded            sync.Once
	credPath          string
)

func init() {
	var err error
	credPath, err = getCredentialPath()
	if err != nil {
		panic(fmt.Sprintf("failed to get credential path: %s", err))
	}
}

func getCredentialPath() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		switch runtime.GOOS {
		case "windows":
			dir = os.Getenv("LocalAppData")
			if dir == "" {
				return "", errors.New("%LocalAppData% is not set")
			}
		case "darwin":
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get user home directory: %w", err)
			}
			dir = filepath.Join(home, "Library")
		default: // unix
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get user home directory: %w", err)
			}
			dir = filepath.Join(home, ".local", "share")
		}
	} else if !filepath.IsAbs(dir) {
		return "", errors.New("path in $XDG_DATA_HOME is relative")
	}

	return filepath.Join(dir, "dbc", "credentials", "credentials.toml"), nil
}

func loadCreds() ([]Credential, error) {
	credFile, err := os.Open(credPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Credential{}, nil
		}
		return nil, err
	}
	defer credFile.Close()

	creds := struct {
		Credentials []Credential `toml:"credentials"`
	}{}

	if err := toml.NewDecoder(credFile).Decode(&creds); err != nil {
		return nil, err
	}

	return creds.Credentials, nil
}

func GetCredentials(u *url.URL) (*Credential, error) {
	if err := LoadCredentials(); err != nil {
		return nil, err
	}

	for i, cred := range loadedCredentials {
		if cred.RegistryURL.Host == u.Host {
			return &loadedCredentials[i], nil
		}
	}

	return nil, nil
}

func LoadCredentials() error {
	loaded.Do(func() {
		loadedCredentials, credentialErr = loadCreds()
	})
	return credentialErr
}

func AddCredential(cred Credential, allowOverwrite bool) error {
	if err := LoadCredentials(); err != nil {
		return err
	}

	idx := slices.IndexFunc(loadedCredentials, func(c Credential) bool {
		return c.RegistryURL.Host == cred.RegistryURL.Host
	})

	if idx != -1 {
		if !allowOverwrite {
			return fmt.Errorf("credentials for %s already exist", cred.RegistryURL.Host)
		}
		loadedCredentials[idx] = cred
	} else {
		loadedCredentials = append(loadedCredentials, cred)
	}
	return UpdateCreds()
}

func RemoveCredential(host Uri) error {
	if err := LoadCredentials(); err != nil {
		return err
	}

	idx := slices.IndexFunc(loadedCredentials, func(c Credential) bool {
		return c.RegistryURL.Host == host.Host
	})

	if idx == -1 {
		return fmt.Errorf("no credentials found for %s", host.Host)
	}

	loadedCredentials = append(loadedCredentials[:idx], loadedCredentials[idx+1:]...)
	return UpdateCreds()
}

func UpdateCreds() error {
	if err := LoadCredentials(); err != nil {
		return err
	}

	err := os.MkdirAll(filepath.Dir(credPath), 0o700)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(credPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	return toml.NewEncoder(f).Encode(struct {
		Credentials []Credential `toml:"credentials"`
	}{
		Credentials: loadedCredentials,
	})
}

func PurgeCredentials() error {
	var fileList = []string{
		"credentials.toml",
		"columnar.lic",
	}

	prefix := filepath.Dir(credPath)

	for _, file := range fileList {
		fullPath := filepath.Join(prefix, file)
		if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	return nil
}

func IsColumnarPrivateRegistry(u *url.URL) bool {
	return u.Host == DefaultOauthURI
}

const licenseURI = "https://heimdall.columnar.tech/trial_license"

var (
	ErrNoTrialLicense = errors.New("no trial license found")
	ErrTrialExpired   = errors.New("trial license has expired")
)

func FetchColumnarLicense(cred *Credential) error {
	licensePath := filepath.Join(filepath.Dir(credPath), "columnar.lic")
	_, err := os.Stat(licensePath)
	if err == nil { // license exists already
		return nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	var authToken string
	switch cred.Type {
	case TypeApiKey:
		authToken = cred.ApiKey
	case TypeToken:
		p := jwt.NewParser()
		tk, err := p.Parse(cred.GetAuthToken(), nil)
		if err != nil && !errors.Is(err, jwt.ErrTokenUnverifiable) {
			return fmt.Errorf("failed to parse oauth token: %w", err)
		}

		_, ok := tk.Claims.(jwt.MapClaims)["urn:columnar:trial_start"]
		if !ok {
			return ErrNoTrialLicense
		}
		authToken = cred.GetAuthToken()
	default:
		return fmt.Errorf("unsupported credential type: %s", cred.Type)
	}

	req, err := http.NewRequest(http.MethodGet, licenseURI, nil)
	if err != nil {
		return err
	}

	req.Header.Add("authorization", "Bearer "+authToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusBadRequest:
			return ErrNoTrialLicense
		case http.StatusForbidden:
			return ErrTrialExpired
		default:
			return fmt.Errorf("failed to fetch license: %s", resp.Status)
		}
	}

	licenseFile, err := os.OpenFile(licensePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer licenseFile.Close()
	if _, err = licenseFile.ReadFrom(resp.Body); err != nil {
		licenseFile.Close()
		os.Remove(licensePath)
	}
	return err
}
