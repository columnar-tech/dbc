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

//go:build js

package fslock_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/columnar-tech/dbc/internal/fslock"
)

func TestAcquireContextSerializesSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	first, err := fslock.AcquireContext(context.Background(), path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan fslock.Lock, 1)
	failed := make(chan error, 1)
	go func() {
		lock, err := fslock.AcquireContext(context.Background(), path, time.Second)
		if err != nil {
			failed <- err
			return
		}
		acquired <- lock
	}()
	select {
	case lock := <-acquired:
		lock.Release()
		t.Fatal("second lock acquired before the first was released")
	case err := <-failed:
		t.Fatalf("second AcquireContext: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case lock := <-acquired:
		if err := lock.Release(); err != nil {
			t.Fatal(err)
		}
	case err := <-failed:
		t.Fatalf("second AcquireContext: %v", err)
	case <-time.After(time.Second):
		t.Fatal("second lock did not acquire after release")
	}
}

func TestAcquireContextCancellationWhileWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	first, err := fslock.AcquireContext(context.Background(), path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := fslock.AcquireContext(ctx, path, time.Second)
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("AcquireContext error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AcquireContext did not return after cancellation")
	}
}

func TestAcquireContextTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	first, err := fslock.AcquireContext(context.Background(), path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	_, err = fslock.AcquireContext(context.Background(), path, 20*time.Millisecond)
	if !errors.Is(err, fslock.ErrLockContended) {
		t.Fatalf("AcquireContext error = %v, want ErrLockContended", err)
	}
}
