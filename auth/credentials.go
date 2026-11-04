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
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/columnar-tech/dbc/internal"
	"github.com/columnar-tech/dbc/internal/fslock"
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

func (t *Credential) Refresh(ctx context.Context) error {
	switch t.Type {
	case TypeApiKey:
		req, err := http.NewRequestWithContext(ctx,
			http.MethodGet, (*url.URL)(&t.AuthURI).String(), nil)
		if err != nil {
			return fmt.Errorf("apikey refresh: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+t.ApiKey)

		rsp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("apikey refresh: %w", err)
		}
		defer rsp.Body.Close()
		if rsp.StatusCode != http.StatusOK {
			return fmt.Errorf("apikey refresh: status %s", rsp.Status)
		}

		var tokenResp struct {
			Token string `json:"access_token"`
		}
		if err := json.NewDecoder(rsp.Body).Decode(&tokenResp); err != nil {
			return err
		}

		t.Token = tokenResp.Token
		return nil
	case TypeToken:
		if err := refreshOauth(ctx, t); err != nil {
			return fmt.Errorf("oauth refresh: %w", err)
		}
		return nil
	}

	return fmt.Errorf("unsupported credential type: %s", t.Type)
}

// RefreshAndPersist refreshes t and, if t came from the credential store,
// writes the refreshed credential back (see persistRefreshed). Persisting is
// best effort: the refreshed token is used even if the write fails.
// Must not be called while credMu is held.
func (t *Credential) RefreshAndPersist(ctx context.Context) error {
	before := *t
	if err := t.Refresh(ctx); err != nil {
		return err
	}
	_ = persistRefreshed(before, t)
	return nil
}

// GetAuthToken returns the current token, refreshing if needed.
// Must not be called while credMu is held.
func (t *Credential) GetAuthToken(ctx context.Context) string {
	if t.Token != "" {
		return t.Token
	}

	if err := t.RefreshAndPersist(ctx); err == nil {
		return t.Token
	}

	return ""
}

var (
	loadedCredentials []Credential
	// storeIssued holds every *Credential GetCredentials has returned. Writes
	// replace loadedCredentials, so pointer membership in the current slice
	// cannot tell whether a credential came from the store.
	storeIssued   sync.Map // *Credential -> struct{}
	credentialErr error
	loaded        sync.Once
	credPath      string
	credPathMu    sync.Mutex
	credMu        sync.RWMutex
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
			storeIssued.Store(&loadedCredentials[i], struct{}{})
			return &loadedCredentials[i], nil
		}
	}

	return nil, nil
}

func LoadCredentials() error {
	loaded.Do(func() {
		credMu.Lock()
		creds, err := loadCreds()
		loadedCredentials, credentialErr = creds, err
		credMu.Unlock()
	})
	credMu.RLock()
	defer credMu.RUnlock()
	return credentialErr
}

// credentialLockTimeout bounds how long a writer waits for another process.
var credentialLockTimeout = 10 * time.Second

// lockCredentialFile takes the cross-process lock for read-modify-write
// updates of the credentials file. Take it before credMu. On JavaScript/Wasm,
// fslock only excludes callers within one Go/Wasm runtime.
func lockCredentialFile() (release func(), err error) {
	cp, err := getCredPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get credential path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cp), 0o700); err != nil {
		return nil, err
	}
	lock, err := fslock.Acquire(cp+".lock", credentialLockTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to lock credentials file: %w", err)
	}
	return func() { _ = lock.Release() }, nil
}

func AddCredential(cred Credential, allowOverwrite bool) error {
	release, err := lockCredentialFile()
	if err != nil {
		return err
	}
	defer release()
	credMu.Lock()
	defer credMu.Unlock()

	creds, err := loadCreds()
	if err != nil {
		return err
	}

	idx := slices.IndexFunc(creds, func(c Credential) bool {
		return c.RegistryURL.Host == cred.RegistryURL.Host
	})

	if idx != -1 {
		if !allowOverwrite {
			return fmt.Errorf("credentials for %s already exist", cred.RegistryURL.Host)
		}
		creds[idx] = cred
	} else {
		creds = append(creds, cred)
	}
	return writeCreds(creds)
}

func RemoveCredential(host Uri) error {
	release, err := lockCredentialFile()
	if err != nil {
		return err
	}
	defer release()
	credMu.Lock()
	defer credMu.Unlock()

	creds, err := loadCreds()
	if err != nil {
		return err
	}

	idx := slices.IndexFunc(creds, func(c Credential) bool {
		return c.RegistryURL.Host == host.Host
	})

	if idx == -1 {
		return fmt.Errorf("no credentials found for %s", host.Host)
	}

	creds = append(creds[:idx], creds[idx+1:]...)
	return writeCreds(creds)
}

// writeCreds persists creds, then updates the lookup cache on success.
// Caller must hold credMu.
func writeCreds(creds []Credential) error {
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

	if err := toml.NewEncoder(f).Encode(struct {
		Credentials []Credential `toml:"credentials"`
	}{
		Credentials: creds,
	}); err != nil {
		return err
	}
	loadedCredentials, credentialErr = creds, nil
	return nil
}

// UpdateCreds rewrites the credentials file from this process's cached credentials.
//
// Deprecated: This overwrites changes other processes made since this process
// loaded credentials. Use AddCredential or RemoveCredential, which re-read the
// file before applying changes.
func UpdateCreds() error {
	if err := LoadCredentials(); err != nil {
		return err
	}

	credMu.Lock()
	defer credMu.Unlock()
	return writeCreds(loadedCredentials)
}

// persistRefreshed writes cred, refreshed from before, back to the store. It
// merges by host into the latest file contents, so other processes' changes
// survive: it skips credentials not from the store (e.g. injected by a
// binding), hosts removed since, and hosts whose stored entry no longer
// matches before because another process logged in again.
func persistRefreshed(before Credential, cred *Credential) error {
	// Checked first so injected credentials never load or touch the store.
	if _, fromStore := storeIssued.Load(cred); !fromStore {
		return nil
	}

	release, err := lockCredentialFile()
	if err != nil {
		return err
	}
	defer release()
	credMu.Lock()
	defer credMu.Unlock()

	creds, err := loadCreds()
	if err != nil {
		return err
	}
	idx := slices.IndexFunc(creds, func(c Credential) bool {
		return c.RegistryURL.Host == cred.RegistryURL.Host
	})
	if idx == -1 || !reflect.DeepEqual(creds[idx], before) {
		return nil
	}
	creds[idx] = *cred
	return writeCreds(creds)
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

	release, err := lockCredentialFile()
	if err != nil {
		return err
	}
	defer release()

	for _, file := range fileList {
		fullPath := filepath.Join(prefix, file)
		if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	// Remove the lock file last, while holding it: a waiter that then wins
	// the lock on the unlinked file sees it is stale and reopens. On Windows
	// the open handle blocks removal, and Release deletes it instead.
	_ = os.Remove(cp + ".lock")
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

func LicensePath() (string, error) {
	cp, err := getCredPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(cp), "columnar.lic"), nil
}

func InstallLicenseFromFile(srcPath string, force bool) error {
	if !force && filepath.Base(srcPath) != "columnar.lic" {
		return ErrLicenseWrongFilename
	}

	destPath, err := LicensePath()
	if err != nil {
		return fmt.Errorf("failed to determine license path: %w", err)
	}

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

func FetchColumnarLicense(ctx context.Context, cred *Credential) error {
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
		tk, err := p.Parse(cred.GetAuthToken(ctx), nil)
		if err != nil && !errors.Is(err, jwt.ErrTokenUnverifiable) {
			return fmt.Errorf("failed to parse oauth token: %w", err)
		}

		_, ok := tk.Claims.(jwt.MapClaims)["urn:columnar:trial_start"]
		if !ok {
			return ErrNoTrialLicense
		}
		authToken = cred.GetAuthToken(ctx)
	default:
		return fmt.Errorf("unsupported credential type: %s", cred.Type)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, licenseURI, nil)
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

	tmp, err := os.CreateTemp(filepath.Dir(licensePath), ".lic.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("write license: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close license temp file: %w", err)
	}
	return os.Rename(tmpName, licensePath)
}
