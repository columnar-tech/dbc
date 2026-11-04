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

//go:build !js

package fslock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func markerFor(t *testing.T, lockPath string) string {
	t.Helper()
	canonical, err := canonicalLockPath(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	return markerPath(canonical)
}

// A Go/Wasm runtime holds the lock by its marker alone; native dbc must wait
// for it even though no OS lock is held.
func TestNativeAcquireContendsWithAWasmHolder(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")
	marker := markerFor(t, lockPath)
	if err := os.WriteFile(marker, nil, 0o644); err != nil { // as a Wasm runtime creates it
		t.Fatal(err)
	}
	if _, err := Acquire(lockPath, 50*time.Millisecond); !errors.Is(err, ErrLockContended) {
		t.Fatalf("Acquire with a live Wasm marker: err = %v, want ErrLockContended", err)
	}
	// The failed attempt must not have kept the OS lock.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(lockPath, 0)
	if err != nil {
		t.Fatalf("Acquire after the Wasm holder released: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

// Only the OS lock holder creates a native marker, so one found while holding
// the OS lock was left by a native process that died; it must not block for
// the staleness window.
func TestNativeAcquireRemovesADeadNativeMarkerAtOnce(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")
	if err := os.WriteFile(markerFor(t, lockPath), nativeMarkerOwner, 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(lockPath, 0)
	if err != nil {
		t.Fatalf("Acquire with a dead native marker: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeLockHoldsAndReleasesItsMarker(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")
	marker := markerFor(t, lockPath)
	lock, err := Acquire(lockPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != string(nativeMarkerOwner) {
		t.Fatalf("marker while held = %q, %v; want the native owner", content, err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker after Release: %v, want it removed", err)
	}
}

// Lock names may already be as long as a file name can be; the marker must
// still fit.
func TestMarkerNameFitsForMaximumLengthLockNames(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, strings.Repeat("a", 255))
	marker := markerFor(t, lockPath)
	if n := len(filepath.Base(marker)); n > maxMarkerName {
		t.Fatalf("marker name is %d bytes, longer than %d", n, maxMarkerName)
	}
	if other := markerFor(t, filepath.Join(dir, strings.Repeat("a", 254)+"b")); other == marker {
		t.Fatal("distinct long lock names share a marker")
	}
	lock, err := Acquire(lockPath, 0)
	if err != nil {
		t.Fatalf("Acquire with a maximum-length name: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}
