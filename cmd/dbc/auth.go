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
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/cli/oauth/device"
	"github.com/columnar-tech/dbc/auth"
)

type AuthCmd struct {
	Login  *LoginCmd  `arg:"subcommand" help:"Authenticate with a driver index"`
	Logout *LogoutCmd `arg:"subcommand" help:"Logout from a driver index"`
}

type LoginCmd struct {
	URI      string `arg:"positional" help:"URL of the driver index to authenticate with"`
	ClientID string `arg:"env:OAUTH_CLIENT_ID" help:"OAuth Client ID (can also be set via DBC_OAUTH_CLIENT_ID)"`
	Audience string `arg:"env:OAUTH_AUDIENCE" help:"OAuth Audience (can also be set via DBC_OAUTH_AUDIENCE)"`
	ApiKey   string `arg:"--api-key" help:"Authenticate using an API key instead of OAuth"`
}

func (l LoginCmd) GetModelCustom(baseModel baseModel) tea.Model {
	if l.URI == "" {
		l.URI = auth.DefaultOauthURI
	}

	if l.URI == auth.DefaultOauthURI {
		if l.ClientID == "" {
			l.ClientID = auth.DefaultOauthClientID
		}
		if l.Audience == "" {
			l.Audience = auth.DefaultOauthAudience
		}
	}

	s := spinner.New()
	s.Spinner = spinner.MiniDot
	return loginModel{
		spinner:       s,
		inputURI:      l.URI,
		oauthClientID: l.ClientID,
		oauthAudience: l.Audience,
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
	oauthAudience string
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
		rsp, err := device.RequestCode(http.DefaultClient, cfg.DeviceAuthorizationEndpoint.String(),
			m.oauthClientID, []string{"openid", "offline_access"},
			device.WithAudience(m.oauthAudience))
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
			Type:     auth.TypeApiKey,
			IndexURI: auth.Uri(*m.parsedURI),
			AuthURI:  auth.Uri(*loginURL),
			ApiKey:   m.apiKey,
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
				accessToken, err := device.Wait(context.TODO(), http.DefaultClient, m.tokenURI.String(), device.WaitOptions{
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
					Audience:     m.oauthAudience,
					IndexURI:     auth.Uri(*m.parsedURI),
					RefreshToken: accessToken.RefreshToken,
				}
			})
	case auth.Credential:
		return m, tea.Sequence(func() tea.Msg {
			if err := auth.AddCredential(msg); err != nil {
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
	URI string `arg:"positional" help:"URL of the driver index to logout from"`
}

func (l LogoutCmd) GetModelCustom(baseModel baseModel) tea.Model {
	if l.URI == "" {
		l.URI = auth.DefaultOauthURI
	}

	return logoutModel{
		inputURI:  l.URI,
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
		return m, tea.Sequence(func() tea.Msg {
			return auth.RemoveCredential(auth.Uri(*msg))
		}, tea.Quit)
	}

	base, cmd := m.baseModel.Update(msg)
	m.baseModel = base.(baseModel)
	return m, cmd
}

func (m logoutModel) View() string { return "" }
