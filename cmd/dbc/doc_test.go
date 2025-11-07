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
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockOpenBrowserFunc(openedURL *string) func(string) error {
	return func(url string) error {
		*openedURL = url
		return nil
	}
}

func mockOpenBrowserFuncError() func(string) error {
	return func(url string) error {
		return fmt.Errorf("browser not available")
	}
}

var testFallbackUrls = map[string]string{
	"test-driver-1": "https://test.example.com/driver1",
	"test-driver-2": "https://test.example.com/driver2",
}

func TestDocNoDriver(t *testing.T) {
	var openedURL string

	m := DocCmd{Driver: ""}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		true, // Headless mode
		mockOpenBrowserFunc(&openedURL),
		testFallbackUrls,
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithContext(ctx))

	m, err := p.Run()
	require.NoError(t, err)
	assert.Equal(t, 0, m.(HasStatus).Status())
	assert.Contains(t, out.String(), "https://docs.columnar.tech/dbc/")
}

func TestDocNoDriverInteractiveSayYes(t *testing.T) {
	var openedURL string

	m := DocCmd{Driver: ""}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		false, // Interactive mode
		mockOpenBrowserFunc(&openedURL),
		testFallbackUrls,
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var in bytes.Buffer
	var out bytes.Buffer
	in.WriteString("y\n")

	p := tea.NewProgram(m, tea.WithInput(&in), tea.WithOutput(&out),
		tea.WithContext(ctx))

	m, err := p.Run()
	require.NoError(t, err)
	assert.Equal(t, 0, m.(HasStatus).Status())
	assert.Contains(t, out.String(), "Opening documentation in browser...")
	assert.Equal(t, "https://docs.columnar.tech/dbc/", openedURL)
}

func TestDocNoDriverInteractiveSayNo(t *testing.T) {
	var openedURL string

	m := DocCmd{Driver: ""}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		false, // Interactive mode
		mockOpenBrowserFunc(&openedURL),
		testFallbackUrls,
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var in bytes.Buffer
	var out bytes.Buffer
	in.WriteString("n\n")

	p := tea.NewProgram(m, tea.WithInput(&in), tea.WithOutput(&out),
		tea.WithContext(ctx))

	m, err := p.Run()
	require.NoError(t, err)
	assert.Equal(t, 0, m.(HasStatus).Status())
	assert.NotContains(t, out.String(), "Opening documentation in browser...")
	assert.Equal(t, "", openedURL)
}

func TestDocNoDriverInteractiveDecline(t *testing.T) {
	var openedURL string

	m := DocCmd{Driver: ""}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		false, // Interactive mode
		mockOpenBrowserFunc(&openedURL),
		testFallbackUrls,
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var in bytes.Buffer
	var out bytes.Buffer
	in.WriteString("n\n")

	p := tea.NewProgram(m, tea.WithInput(&in), tea.WithOutput(&out),
		tea.WithContext(ctx))

	m, err := p.Run()
	require.NoError(t, err)
	assert.Equal(t, 0, m.(HasStatus).Status())
	assert.NotContains(t, out.String(), "Opening documentation in browser...")
	assert.Equal(t, "", openedURL)
}

func TestDocDriverFound(t *testing.T) {
	var openedURL string

	m := DocCmd{Driver: "test-driver-1"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		true, // Headless mode
		mockOpenBrowserFunc(&openedURL),
		testFallbackUrls,
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithContext(ctx))

	m, err := p.Run()
	require.NoError(t, err)
	assert.Equal(t, 0, m.(HasStatus).Status())
	assert.Contains(t, out.String(), "https://test.example.com/driver1")
}

func TestDocDriverFoundInteractive(t *testing.T) {
	var openedURL string

	m := DocCmd{Driver: "test-driver-1"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		false, // Interactive mode
		mockOpenBrowserFunc(&openedURL),
		testFallbackUrls,
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var in bytes.Buffer
	var out bytes.Buffer

	p := tea.NewProgram(m, tea.WithInput(&in), tea.WithOutput(&out),
		tea.WithContext(ctx))

	var finalModel tea.Model
	var runErr error
	go func() { finalModel, runErr = p.Run() }()

	<-time.After(time.Millisecond * 500)
	require.NoError(t, ctx.Err())

	// Send the key message
	in.Write([]byte("y"))
	p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})

	p.Wait()
	require.NoError(t, runErr)
	assert.Equal(t, 0, finalModel.(HasStatus).Status())
	assert.Contains(t, out.String(), "Opening documentation in browser...")
	assert.Equal(t, "https://test.example.com/driver1", openedURL)
}

func TestDocDriverNotFound(t *testing.T) {
	var openedURL string

	m := DocCmd{Driver: "nonexistent-driver"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		true, // Headless mode
		mockOpenBrowserFunc(&openedURL),
		testFallbackUrls,
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithContext(ctx))

	m, err := p.Run()
	require.NoError(t, err)
	assert.Equal(t, 1, m.(HasStatus).Status())
	assert.Contains(t, out.String(), "driver `nonexistent-driver` not found in driver registry index")
}

func TestDocDriverNotInFallbackMap(t *testing.T) {
	var openedURL string

	m := DocCmd{Driver: "test-driver-2"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		true, // Headless mode
		mockOpenBrowserFunc(&openedURL),
		testFallbackUrls,
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var out bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out),
		tea.WithContext(ctx))

	m, err := p.Run()
	require.NoError(t, err)
	assert.Equal(t, 0, m.(HasStatus).Status())
	assert.Contains(t, out.String(), "https://test.example.com/driver2")
}

func TestDocBrowserOpenError(t *testing.T) {
	m := DocCmd{Driver: "test-driver-1"}.GetModelCustom(
		baseModel{
			getDriverList: getTestDriverList,
			downloadPkg:   downloadTestPkg,
		},
		false, // Interactive mode
		mockOpenBrowserFuncError(),
		testFallbackUrls,
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var in bytes.Buffer
	var out bytes.Buffer

	p := tea.NewProgram(m, tea.WithInput(&in), tea.WithOutput(&out),
		tea.WithContext(ctx))

	var finalModel tea.Model
	var runErr error
	go func() { finalModel, runErr = p.Run() }()

	<-time.After(time.Millisecond * 500)
	require.NoError(t, ctx.Err())

	// Send the key message
	in.Write([]byte("y"))
	p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})

	p.Wait()
	require.NoError(t, runErr)
	assert.Equal(t, 1, finalModel.(HasStatus).Status())
	assert.Contains(t, out.String(), "failed to open browser: browser not available")
}
