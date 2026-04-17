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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/cli/browser"
	"github.com/cli/oauth/device"
	"github.com/columnar-tech/dbc/auth"
)

func ensureHTTPS(uri string) string {
	if !strings.HasPrefix(uri, "https://") {
		return "https://" + uri
	}
	return uri
}

type AuthCmd struct {
	Login   *LoginCmd   `arg:"subcommand" help:"Authenticate with a driver registry"`
	Logout  *LogoutCmd  `arg:"subcommand" help:"Log out from a driver registry"`
	License *LicenseCmd `arg:"subcommand" help:"Manage license files"`
}

type LicenseCmd struct {
	Install *LicenseInstallCmd `arg:"subcommand" help:"Install a license file"`
}

type LicenseInstallCmd struct {
	LicensePath string `arg:"positional,required" help:"Path to the license file to install"`
	Force       bool   `arg:"--force" help:"Overwrite existing license and skip filename check"`
}

type LoginCmd struct {
	RegistryURL string `arg:"positional" help:"URL of the driver registry to authenticate with [default: https://dbc-cdn-private.columnar.tech]"`
	ClientID    string `arg:"env:OAUTH_CLIENT_ID" help:"OAuth Client ID (can also be set via DBC_OAUTH_CLIENT_ID)"`
	ApiKey      string `arg:"--api-key" help:"Authenticate using an API key instead of OAuth (use '-' to read from stdin)"`
}

func (l LoginCmd) GetModelCustom(baseModel baseModel) tea.Model {
	if l.ApiKey == "-" {
		reader := bufio.NewReader(os.Stdin)
		apiKey, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintf(os.Stderr, "failed to read API key from stdin: %s\n", err)
			os.Exit(1)
		}

		l.ApiKey = strings.TrimSpace(apiKey)
	}

	if l.RegistryURL == "" {
		l.RegistryURL = auth.DefaultOauthURI()
	}

	if l.RegistryURL == auth.DefaultOauthURI() {
		if l.ClientID == "" {
			l.ClientID = auth.DefaultOauthClientID()
		}
	}

	s := spinner.New()
	s.Spinner = spinner.MiniDot
	return loginModel{
		spinner:       s,
		inputURI:      l.RegistryURL,
		oauthClientID: l.ClientID,
		apiKey:        l.ApiKey,
		baseModel:     baseModel,
	}
}

func (l LoginCmd) GetModel() tea.Model {
	return l.GetModelCustom(defaultBaseModel())
}

type authSuccessMsg struct {
	cred auth.Credential
}

func (loginModel) NeedsRenderer() {}

type loginModel struct {
	baseModel

	spinner spinner.Model

	inputURI      string
	oauthClientID string
	apiKey        string
	tokenURI      *url.URL
	parsedURI     *url.URL
}

func (m loginModel) Init() tea.Cmd {
	m.inputURI = ensureHTTPS(m.inputURI)

	u, err := url.Parse(m.inputURI)
	if err != nil {
		return errCmd("invalid URI provided: %w", err)
	}

	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		return u
	})
}

func (m loginModel) authConfig() tea.Cmd {
	return func() tea.Msg {
		cfg, err := auth.GetOpenIDConfig(m.parsedURI)
		if err != nil {
			return fmt.Errorf("failed to get OpenID configuration: %w", err)
		}
		return cfg
	}
}

func (m loginModel) requestDeviceCode(cfg auth.OpenIDConfig) tea.Cmd {
	return func() tea.Msg {
		rsp, err := device.RequestCode(dbcClient.HTTPClient(), cfg.DeviceAuthorizationEndpoint.String(),
			m.oauthClientID, []string{"openid", "offline_access"})
		if err != nil {
			return fmt.Errorf("failed to request device code: %w", err)
		}

		return rsp
	}
}

func (m loginModel) apiKeyToToken() tea.Cmd {
	return func() tea.Msg {
		loginURL, _ := m.parsedURI.Parse("/login")
		cred := auth.Credential{
			Type:        auth.TypeApiKey,
			RegistryURL: auth.Uri(*m.parsedURI),
			AuthURI:     auth.Uri(*loginURL),
			ApiKey:      m.apiKey,
		}

		if err := cred.Refresh(); err != nil {
			return fmt.Errorf("failed to obtain access token using provided API key")
		}

		return cred
	}
}

func (m loginModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case *url.URL:
		m.parsedURI = msg
		if m.apiKey != "" {
			return m, m.apiKeyToToken()
		} else {
			return m, m.authConfig()
		}
	case auth.OpenIDConfig:
		m.tokenURI = (*url.URL)(&msg.TokenEndpoint)
		return m, m.requestDeviceCode(msg)
	case *device.CodeResponse:
		return m, tea.Sequence(
			tea.Println("Opening ", msg.VerificationURIComplete, " in your default web browser..."),
			func() tea.Msg {
				browser.OpenURL(msg.VerificationURIComplete)
				accessToken, err := device.Wait(context.TODO(), dbcClient.HTTPClient(), m.tokenURI.String(), device.WaitOptions{
					ClientID:   m.oauthClientID,
					DeviceCode: msg,
				})

				if err != nil {
					return fmt.Errorf("failed to obtain access token: %w", err)
				}

				return auth.Credential{
					Type:         auth.TypeToken,
					AuthURI:      auth.Uri(*m.parsedURI),
					Token:        accessToken.Token,
					ClientID:     m.oauthClientID,
					RegistryURL:  auth.Uri(*m.parsedURI),
					RefreshToken: accessToken.RefreshToken,
				}
			})
	case auth.Credential:
		return m, func() tea.Msg {
			if err := auth.AddCredential(msg, true); err != nil {
				return err
			}
			return authSuccessMsg{cred: msg}
		}
	case authSuccessMsg:
		return m, tea.Sequence(tea.Println("Authentication successful!"),
			func() tea.Msg {
				if auth.IsColumnarPrivateRegistry((*url.URL)(&msg.cred.RegistryURL)) {
					if err := auth.FetchColumnarLicense(&msg.cred); err != nil {
						return err
					}
				}
				return tea.Quit()
			})
	case error:
		switch {
		case errors.Is(msg, auth.ErrTrialExpired) ||
			errors.Is(msg, auth.ErrNoTrialLicense):
			// ignore these errors during auth login
			// the user can still login but won't be able to download trial licenses
			return m, tea.Quit
		default:
			// for other errors, let the baseModel update handle it.
		}
	}

	base, cmd := m.baseModel.Update(msg)
	m.baseModel = base.(baseModel)
	return m, cmd
}

func (m loginModel) View() tea.View {
	return tea.NewView(m.spinner.View() + " Waiting for confirmation...")
}

type LogoutCmd struct {
	RegistryURL string `arg:"positional" help:"URL of the driver registry to log out from [default: https://dbc-cdn-private.columnar.tech]"`
	Purge       bool   `arg:"--purge" help:"Remove all local auth credentials for dbc"`
}

func (l LogoutCmd) GetModelCustom(baseModel baseModel) tea.Model {
	if l.RegistryURL == "" {
		l.RegistryURL = auth.DefaultOauthURI()
	}

	return logoutModel{
		inputURI:  l.RegistryURL,
		baseModel: baseModel,
		purge:     l.Purge,
	}
}

func (l LogoutCmd) GetModel() tea.Model {
	return l.GetModelCustom(defaultBaseModel())
}

type logoutModel struct {
	baseModel

	inputURI string
	purge    bool
}

func (m logoutModel) Init() tea.Cmd {
	m.inputURI = ensureHTTPS(m.inputURI)

	u, err := url.Parse(m.inputURI)
	if err != nil {
		return errCmd("invalid URI provided: %w", err)
	}

	return func() tea.Msg {
		return u
	}
}

func (m logoutModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case *url.URL:
		return m, func() tea.Msg {
			if m.purge {
				if err := auth.PurgeCredentials(); err != nil {
					return fmt.Errorf("failed to purge credentials: %w", err)
				}
			} else {
				if err := auth.RemoveCredential(auth.Uri(*msg)); err != nil {
					return fmt.Errorf("failed to log out: %w", err)
				}
			}

			return tea.QuitMsg{}
		}
	}

	base, cmd := m.baseModel.Update(msg)
	m.baseModel = base.(baseModel)
	return m, cmd
}

func (m logoutModel) View() tea.View { return tea.NewView("") }

func (l LicenseInstallCmd) GetModelCustom(baseModel baseModel) tea.Model {
	return licenseInstallModel{
		baseModel:   baseModel,
		licensePath: l.LicensePath,
		force:       l.Force,
	}
}

func (l LicenseInstallCmd) GetModel() tea.Model {
	return l.GetModelCustom(defaultBaseModel())
}

type licenseInstalledMsg struct{}

type licenseInstallModel struct {
	baseModel

	licensePath string
	force       bool
	installed   bool
}

func (m licenseInstallModel) Init() tea.Cmd {
	return func() tea.Msg {
		if err := auth.InstallLicenseFromFile(m.licensePath, m.force); err != nil {
			return err
		}
		return licenseInstalledMsg{}
	}
}

func (m licenseInstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case licenseInstalledMsg:
		m.installed = true
		return m, tea.Quit
	}

	base, cmd := m.baseModel.Update(msg)
	m.baseModel = base.(baseModel)
	return m, cmd
}

func (m licenseInstallModel) FinalOutput() string {
	if !m.installed {
		return ""
	}
	p, err := auth.LicensePath()
	if err != nil {
		return "License installed (could not determine path)"
	}
	return "License installed to " + p
}

func (m licenseInstallModel) View() tea.View { return tea.NewView("") }
