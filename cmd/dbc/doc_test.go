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
	"os"
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
}

func TestDocCmd(t *testing.T) {
	tests := []struct {
		name              string
		driver            string
		isHeadless        bool
		input             string
		openBrowserFunc   func(*string) func(string) error
		expectedStatus    int
		expectedOpenedURL string
		expectedOutputMsg string
	}{
		// headless, no driver arg
		{
			name:              "headless no driver",
			driver:            "",
			isHeadless:        true,
			openBrowserFunc:   mockOpenBrowserFunc,
			expectedStatus:    0,
			expectedOutputMsg: "https://docs.columnar.tech/dbc/",
		},
		// headless, with driver arg
		{
			name:              "headless driver found in fallback map",
			driver:            "test-driver-1",
			isHeadless:        true,
			openBrowserFunc:   mockOpenBrowserFunc,
			expectedStatus:    0,
			expectedOutputMsg: "https://test.example.com/driver1",
		},
		{
			name:              "headless driver found not in fallback map",
			driver:            "test-driver-2",
			isHeadless:        true,
			openBrowserFunc:   mockOpenBrowserFunc,
			expectedStatus:    1,
			expectedOutputMsg: "no documentation available for driver `test-driver-2`",
		},
		{
			name:              "headless driver not found",
			driver:            "nonexistent-driver",
			isHeadless:        true,
			openBrowserFunc:   mockOpenBrowserFunc,
			expectedStatus:    1,
			expectedOutputMsg: "driver `nonexistent-driver` not found in driver registry index",
		},
		// interactive, no driver arg
		{
			name:              "interactive no driver say yes",
			driver:            "",
			isHeadless:        false,
			input:             "y",
			openBrowserFunc:   mockOpenBrowserFunc,
			expectedStatus:    0,
			expectedOpenedURL: "https://docs.columnar.tech/dbc/",
			expectedOutputMsg: "Opening documentation in browser...",
		},
		{
			name:              "interactive no driver say no",
			driver:            "",
			isHeadless:        false,
			input:             "n",
			openBrowserFunc:   mockOpenBrowserFunc,
			expectedStatus:    0,
			expectedOpenedURL: "",
		},
		// interactive, with driver arg
		{
			name:              "interactive driver found say yes",
			driver:            "test-driver-1",
			isHeadless:        false,
			input:             "y",
			openBrowserFunc:   mockOpenBrowserFunc,
			expectedStatus:    0,
			expectedOpenedURL: "https://test.example.com/driver1",
			expectedOutputMsg: "Opening documentation in browser...",
		},
		{
			name:              "interactive driver found say no",
			driver:            "test-driver-1",
			isHeadless:        false,
			input:             "n",
			openBrowserFunc:   mockOpenBrowserFunc,
			expectedStatus:    0,
			expectedOpenedURL: "",
		},
		{
			name:              "interactive driver not found in fallback map",
			driver:            "test-driver-2",
			isHeadless:        false,
			input:             "y",
			openBrowserFunc:   mockOpenBrowserFunc,
			expectedStatus:    1,
			expectedOpenedURL: "",
			expectedOutputMsg: "no documentation available for driver `test-driver-2`",
		},
		{
			name:              "interactive browser open error",
			driver:            "test-driver-1",
			isHeadless:        false,
			input:             "y",
			openBrowserFunc:   func(*string) func(string) error { return mockOpenBrowserFuncError() },
			expectedStatus:    1,
			expectedOpenedURL: "",
			expectedOutputMsg: "failed to open browser: browser not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var openedURL string

			m := DocCmd{Driver: tt.driver}.GetModelCustom(
				baseModel{
					getDriverList: getTestDriverList,
					downloadPkg:   downloadTestPkg,
				},
				tt.isHeadless,
				tt.openBrowserFunc(&openedURL),
				testFallbackUrls,
			)

			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			var in bytes.Buffer
			var out bytes.Buffer

			if tt.isHeadless && tt.input != "" {
				in.WriteString(tt.input)
			}

			p := tea.NewProgram(m, tea.WithInput(&in), tea.WithOutput(&out),
				tea.WithContext(ctx), tea.WithEnvironment(append(os.Environ(), "TERM=linux")))

			var finalModel tea.Model
			var runErr error

			if !tt.isHeadless {
				go func() { finalModel, runErr = p.Run() }()

				<-time.After(time.Millisecond * 500)
				require.NoError(t, ctx.Err())

				p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.input)})
			} else {
				finalModel, runErr = p.Run()
			}

			p.Wait()

			require.NoError(t, runErr)
			assert.Equal(t, tt.expectedStatus, finalModel.(HasStatus).Status())

			assert.Equal(t, tt.expectedOpenedURL, openedURL)
			if tt.expectedOutputMsg != "" {
				// For headless mode with status 0 (success), check FinalOutput for URLs
				// For everything else (errors, interactive), check output buffer
				if tt.isHeadless && tt.expectedStatus == 0 {
					if fo, ok := finalModel.(HasFinalOutput); ok {
						assert.Contains(t, fo.FinalOutput(), tt.expectedOutputMsg)
					}
				} else {
					assert.Contains(t, out.String(), tt.expectedOutputMsg)
				}
			}
		})
	}
}
