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

//go:build !windows

package main

import (
	"os"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
)

// Work around https://github.com/columnar-tech/dbc/issues/351
//
// suppressTerminalProbeResponses prevents BubbleTea v2's capability probe
// responses from appearing as garbled output in the shell.
//
// BubbleTea v2 queries terminal capabilities on startup (DECRQM for mode 2026
// = synchronized output and mode 2027 = unicode core). For fast-completing
// operations like local package installs, the program can exit before the
// terminal's responses arrive in the tty buffer. When the terminal is restored
// to cooked mode (echo on) after the renderer exits, those response bytes get
// echoed to the screen, producing garbled output like "^[[?2026;2$y".
//
// We put the terminal back into raw mode (no echo) immediately after the
// renderer exits so that any in-flight responses are not echoed. Then we sleep
// briefly to let those responses arrive, and drain the buffer with a
// non-blocking syscall.Read loop.
//
// syscall.Read is used directly rather than os.Stdin.Read because Go's file
// wrapper retries EAGAIN through the runtime poller, defeating the
// non-blocking intent.
func suppressTerminalProbeResponses() {
	fd := uintptr(os.Stdin.Fd())

	// Put the terminal back into raw mode so that any in-flight probe
	// responses arriving during the sleep below are not echoed to the screen.
	// If stdin is not a terminal (e.g. piped input), MakeRaw returns an error
	// and we bail.
	state, err := term.MakeRaw(fd)
	if err != nil {
		return
	}
	defer term.Restore(fd, state) //nolint:errcheck

	// Sleep briefly to give the terminal time to deliver its responses.
	// The local terminal round-trip is typically <5ms; 50ms gives headroom.
	time.Sleep(50 * time.Millisecond)

	// Drain whatever arrived in the buffer.
	if err := syscall.SetNonblock(int(fd), true); err != nil {
		return
	}
	defer syscall.SetNonblock(int(fd), false) //nolint:errcheck
	var buf [256]byte
	for {
		if _, err := syscall.Read(int(fd), buf[:]); err != nil {
			return
		}
	}
}
