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

package fslock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/columnar-tech/dbc/internal/hostpath"
)

// The marker file is the part of a lock that both builds take. Node cannot
// take flock/LockFileEx locks, so a Go/Wasm runtime creates the marker with
// O_EXCL instead, and native dbc creates it too, after taking its OS lock, so
// that native and Wasm holders exclude each other. While the lock is held a
// heartbeat refreshes the marker's mtime; a marker untouched for
// markerStaleAfter belongs to a holder that died and is removed.
const (
	markerSuffix     = ".js"
	markerStaleAfter = 30 * time.Second
	markerHeartbeat  = markerStaleAfter / 3
	markerPollEvery  = 10 * time.Millisecond
)

// nativeMarkerOwner is the content of a marker native dbc created. Only the
// holder of the OS lock creates one, so a native process that holds the OS
// lock and finds a native marker knows its creator died holding it.
var nativeMarkerOwner = []byte("dbc-native\n")

// canonicalLockPath resolves path's directory so that symlink aliases of one
// directory name one lock.
func canonicalLockPath(path string) (string, error) {
	canonicalPath, err := hostpath.Abs(hostpath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("fslock: resolve %s: %w", path, err)
	}
	// The lock file often does not exist yet, so resolve its existing parent
	// and append the basename.
	parent := hostpath.Dir(canonicalPath)
	resolvedParent, err := hostpath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("fslock: resolve lock directory %s: %w", parent, err)
	}
	return canonicalRuntimePath(hostpath.Join(resolvedParent, hostpath.Base(canonicalPath)), hostpath.IsWindows()), nil
}

// maxMarkerName is the longest file name the marker may have: 255 bytes is
// the common limit (ext4, APFS, NTFS in UTF-16 units), and lock names may
// already be that long.
const maxMarkerName = 255

// markerPath names the marker for the lock at canonicalPath, identically in
// native and Wasm builds: the lock's name plus markerSuffix, or, if that is
// too long for a file name, a fixed-length name derived from it.
func markerPath(canonicalPath string) string {
	name := hostpath.Base(canonicalPath)
	if len(name)+len(markerSuffix) <= maxMarkerName {
		return canonicalPath + markerSuffix
	}
	sum := sha256.Sum256([]byte(name))
	return hostpath.Join(hostpath.Dir(canonicalPath), ".dbc-marker-"+hex.EncodeToString(sum[:20])+markerSuffix)
}

// acquireMarker creates marker with owner as its content, waiting up to
// timeout for another holder's marker to go away or go stale. ownerIsDead,
// if set, reports whether a marker with the given content belongs to a holder
// known to be dead, which is removed without waiting for it to go stale.
func acquireMarker(ctx context.Context, marker, path string, timeout time.Duration, owner []byte, ownerIsDead func([]byte) bool) (release func() error, err error) {
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, werr := f.Write(owner)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				_ = os.Remove(marker)
				return nil, fmt.Errorf("fslock: write %s: %w", marker, werr)
			}
			return holdMarker(marker), nil
		}
		if errors.Is(err, syscall.ENOSYS) {
			// No filesystem (the browser build): nothing outside this runtime
			// can contend, so the runtime token is the whole lock.
			return func() error { return nil }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("fslock: create %s: %w", marker, err)
		}
		if removed, err := removeDeadMarker(marker, ownerIsDead); err != nil {
			return nil, err
		} else if removed {
			continue
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return nil, fmt.Errorf("fslock: could not acquire lock on %s within %s: %w", path, timeout, ErrLockContended)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(markerPollEvery):
		}
	}
}

// removeDeadMarker removes marker if its owner is known dead or it has gone
// stale. Each check is repeated right before removing, to narrow the window in
// which a new holder's marker could be mistaken for the dead one.
func removeDeadMarker(marker string, ownerIsDead func([]byte) bool) (bool, error) {
	info, err := os.Stat(marker)
	if err != nil {
		return false, nil // gone already, or unreadable: retry the create
	}
	dead := time.Since(info.ModTime()) > markerStaleAfter
	if !dead && ownerIsDead != nil {
		if content, err := os.ReadFile(marker); err == nil && ownerIsDead(content) {
			dead = true
		}
	}
	if !dead {
		return false, nil
	}
	again, err := os.Stat(marker)
	if err != nil || !again.ModTime().Equal(info.ModTime()) {
		return false, nil
	}
	if err := os.Remove(marker); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("fslock: remove stale lock marker %s: %w", marker, err)
	}
	return true, nil
}

// holdMarker keeps marker fresh until the returned release removes it. The
// release runs once: Lock is a value type, so it may be released through a
// copy, and a repeated release must not remove a marker another holder has
// created since.
func holdMarker(marker string) func() error {
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(markerHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				os.Chtimes(marker, now, now)
			}
		}
	}()
	var once sync.Once
	return func() (err error) {
		once.Do(func() {
			close(stop)
			<-done
			if rerr := os.Remove(marker); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
				err = fmt.Errorf("fslock: remove %s: %w", marker, rerr)
			}
		})
		return err
	}
}

func isNativeMarker(content []byte) bool { return bytes.Equal(content, nativeMarkerOwner) }
