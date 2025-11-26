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

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/cli/browser"
	"github.com/cli/oauth/device"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/auth"
)

type AuthCmd struct {
	Login  *LoginCmd  `arg:"subcommand" help:"Authenticate with a driver registry"`
	Logout *LogoutCmd `arg:"subcommand" help:"Log out from a driver registry"`
}

type LoginCmd struct {
	RegistryURL string `arg:"positional" help:"URL of the driver registry to authenticate with"`
	ClientID    string `arg:"env:OAUTH_CLIENT_ID" help:"OAuth Client ID (can also be set via DBC_OAUTH_CLIENT_ID)"`
	ApiKey      string `arg:"--api-key" help:"Authenticate using an API key instead of OAuth (use '-' to read from stdin)"`
}

func (l LoginCmd) GetModelCustom(baseModel baseModel) tea.Model {
	if l.ApiKey == "-" {
		reader := bufio.NewReader(os.Stdin)
		apiKey, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			panic(fmt.Errorf("failed to read API key from stdin: %w", err))
		}

		l.ApiKey = strings.TrimSpace(apiKey)
	}

	if l.RegistryURL == "" {
		l.RegistryURL = auth.DefaultOauthURI
	}

	if l.RegistryURL == auth.DefaultOauthURI {
		if l.ClientID == "" {
			l.ClientID = auth.DefaultOauthClientID
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
	return l.GetModelCustom(
		baseModel{
			getDriverRegistry: getDriverRegistry,
			downloadPkg:       downloadPkg,
		},
	)
}

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
	if !strings.HasPrefix(m.inputURI, "https://") {
		m.inputURI = "https://" + m.inputURI
	}

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
		rsp, err := device.RequestCode(dbc.DefaultClient, cfg.DeviceAuthorizationEndpoint.String(),
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

		if !cred.Refresh() {
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
			tea.Println("Copy code: ", msg.UserCode),
			tea.Println("To authenticate, visit: ", msg.VerificationURIComplete),
			func() tea.Msg {
				browser.OpenURL(msg.VerificationURIComplete)
				accessToken, err := device.Wait(context.TODO(), dbc.DefaultClient, m.tokenURI.String(), device.WaitOptions{
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
		return m, tea.Sequence(func() tea.Msg {
			if err := auth.AddCredential(msg, true); err != nil {
				return err
			}
			return nil
		}, tea.Println("Authentication successful!"),
			tea.Quit)
	}

	base, cmd := m.baseModel.Update(msg)
	m.baseModel = base.(baseModel)
	return m, cmd
}

func (m loginModel) View() string { return m.spinner.View() }

type LogoutCmd struct {
	RegistryURL string `arg:"positional" help:"URL of the driver registry to log out from"`
}

func (l LogoutCmd) GetModelCustom(baseModel baseModel) tea.Model {
	if l.RegistryURL == "" {
		l.RegistryURL = auth.DefaultOauthURI
	}

	return logoutModel{
		inputURI:  l.RegistryURL,
		baseModel: baseModel,
	}
}

func (l LogoutCmd) GetModel() tea.Model {
	return l.GetModelCustom(
		baseModel{
			getDriverRegistry: getDriverRegistry,
			downloadPkg:       downloadPkg,
		},
	)
}

type logoutModel struct {
	baseModel

	inputURI string
}

func (m logoutModel) Init() tea.Cmd {
	if !strings.HasPrefix(m.inputURI, "https://") {
		m.inputURI = "https://" + m.inputURI
	}

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
			if err := auth.RemoveCredential(auth.Uri(*msg)); err != nil {
				return fmt.Errorf("failed to log out: %w", err)
			}

			return tea.QuitMsg{}
		}
	}

	base, cmd := m.baseModel.Update(msg)
	m.baseModel = base.(baseModel)
	return m, cmd
}

func (m logoutModel) View() string { return "" }
