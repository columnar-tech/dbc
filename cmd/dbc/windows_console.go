//go:build windows

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
	"os"

	"golang.org/x/sys/windows"
)

const (
	ENABLE_VIRTUAL_TERMINAL_PROCESSING = 0x0004
)

func initWindowsConsole() {
	if !enableVTProcessing(os.Stdout) || !enableVTProcessing(os.Stderr) {
		disableColors()
		return
	}
}

// enableVTProcessing attempts to enable ENABLE_VIRTUAL_TERMINAL_PROCESSING
// on the given file handle. Returns true if successful or already enabled.
func enableVTProcessing(f *os.File) bool {
	handle := windows.Handle(f.Fd())
	var mode uint32

	// Get current console mode
	err := windows.GetConsoleMode(handle, &mode)
	if err != nil {
		return false
	}

	// Check if VT processing is already enabled
	if mode&ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}

	// Try to enable VT processing
	newMode := mode | ENABLE_VIRTUAL_TERMINAL_PROCESSING
	err = windows.SetConsoleMode(handle, newMode)
	if err != nil {
		return false
	}

	return true
}

// disableColors sets the NO_COLOR environment variable to disable ANSI colors.
// lipgloss and colorprofile respect NO_COLOR automatically.
func disableColors() {
	os.Setenv("NO_COLOR", "1")
}
