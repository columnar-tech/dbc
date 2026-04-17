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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/columnar-tech/dbc/internal"
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
		req, err := http.NewRequestWithContext(context.Background(),
			http.MethodGet, (*url.URL)(&t.AuthURI).String(), nil)
		if err != nil {
			return false
		}
		req.Header.Set("Authorization", "Bearer "+t.ApiKey)

		rsp, err := http.DefaultClient.Do(req)
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
	credPathMu        sync.Mutex
	credMu            sync.RWMutex
)

func getCredPath() (string, error) {
	credPathMu.Lock()
	defer credPathMu.Unlock()
	if credPath == "" {
		var err error
		credPath, err = internal.GetCredentialPath()
		if err != nil {
			return "", fmt.Errorf("failed to get credential path: %w", err)
		}
	}
	return credPath, nil
}

func loadCreds() ([]Credential, error) {
	cp, err := getCredPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get credential path: %w", err)
	}

	credFile, err := os.Open(cp)
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

	credMu.RLock()
	defer credMu.RUnlock()
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

	credMu.Lock()
	defer credMu.Unlock()

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
	return writeCreds()
}

func RemoveCredential(host Uri) error {
	if err := LoadCredentials(); err != nil {
		return err
	}

	credMu.Lock()
	defer credMu.Unlock()

	idx := slices.IndexFunc(loadedCredentials, func(c Credential) bool {
		return c.RegistryURL.Host == host.Host
	})

	if idx == -1 {
		return fmt.Errorf("no credentials found for %s", host.Host)
	}

	loadedCredentials = append(loadedCredentials[:idx], loadedCredentials[idx+1:]...)
	return writeCreds()
}

func writeCreds() error {
	cp, err := getCredPath()
	if err != nil {
		return fmt.Errorf("failed to get credential path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(cp), 0o700); err != nil {
		return err
	}

	f, err := os.OpenFile(cp, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
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

func UpdateCreds() error {
	if err := LoadCredentials(); err != nil {
		return err
	}

	credMu.Lock()
	defer credMu.Unlock()
	return writeCreds()
}

func PurgeCredentials() error {
	cp, err := getCredPath()
	if err != nil {
		return fmt.Errorf("failed to get credential path: %w", err)
	}

	var fileList = []string{
		"credentials.toml",
		"columnar.lic",
	}

	prefix := filepath.Dir(cp)

	for _, file := range fileList {
		fullPath := filepath.Join(prefix, file)
		if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	return nil
}

func IsColumnarPrivateRegistry(u *url.URL) bool {
	return u.Host == defaultOauthURI
}

var (
	ErrNoTrialLicense       = errors.New("no trial license found")
	ErrTrialExpired         = errors.New("trial license has expired")
	ErrLicenseWrongFilename = errors.New("source file is not named columnar.lic (use --force to override)")
	ErrLicenseAlreadyExists = errors.New("license already exists (use --force to overwrite)")
)

func LicensePath() string {
	cp, _ := getCredPath()
	return filepath.Join(filepath.Dir(cp), "columnar.lic")
}

func InstallLicenseFromFile(srcPath string, force bool) error {
	if !force && filepath.Base(srcPath) != "columnar.lic" {
		return ErrLicenseWrongFilename
	}

	destPath := LicensePath()

	if !force {
		if _, err := os.Stat(destPath); err == nil {
			return ErrLicenseAlreadyExists
		}
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("failed to read license file: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return fmt.Errorf("failed to create credentials directory: %w", err)
	}

	if err := os.WriteFile(destPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write license file: %w", err)
	}

	return nil
}

func FetchColumnarLicense(cred *Credential) error {
	cp, err := getCredPath()
	if err != nil {
		return fmt.Errorf("failed to get credential path: %w", err)
	}

	licensePath := filepath.Join(filepath.Dir(cp), "columnar.lic")
	_, err = os.Stat(licensePath)
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
		os.Remove(licensePath)
	}
	return err
}
