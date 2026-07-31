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

//go:build windows

package main

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Work around https://github.com/columnar-tech/dbc/issues/351
//
// suppressTerminalProbeResponses prevents BubbleTea v2's capability probe
// responses from appearing as garbled output in the shell. See drain_unix.go
// for more detail on the problem.
//
// Unix restores raw mode and drains the tty with non-blocking reads. Windows
// has no tty to drain: the fix is to discard the pending console input records
// with FlushConsoleInputBuffer before we exit.
//
// Note this also discards genuine type-ahead the user entered while the command
// was running. That matches the Unix drain, and is the accepted tradeoff: we
// cannot distinguish a user's keystrokes from a terminal's DECRPM reply without
// parsing the input records, and leaking escape sequences into the shell prompt
// is the worse failure.
func suppressTerminalProbeResponses() {
	h := windows.Handle(os.Stdin.Fd())

	// GetConsoleMode fails when stdin is not a console (piped or redirected
	// input). In that case nothing was probed and nothing is buffered.
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return
	}

	// Suppress echo while we wait, so a reply that arrives during the sleep is
	// not written to the screen by the console host before we can flush it.
	// This is belt-and-suspenders — the flush below is what actually fixes the
	// reported symptom — so a failure here is not worth abandoning the flush.
	if quiet := mode &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT); quiet != mode {
		if err := windows.SetConsoleMode(h, quiet); err == nil {
			defer windows.SetConsoleMode(h, mode) //nolint:errcheck
		}
	}

	// Give in-flight responses time to land in the input buffer before we
	// discard it. The local round-trip is typically <5ms; 50ms gives headroom.
	// Matches the Unix implementation in drain_unix.go.
	time.Sleep(50 * time.Millisecond)

	windows.FlushConsoleInputBuffer(h) //nolint:errcheck
}
