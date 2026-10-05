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

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package atomicfile

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteFilePreservingModeKeepsExistingPermissionBits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.toml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFilePreservingMode(path, []byte("new"), 0o666); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("replacement permission bits = %o, want 600", got)
	}
}

func TestPreservingTempPermissionsProtectExistingDestination(t *testing.T) {
	create, final, chmod := preservingTempPermissions(true, 0o640, 0o666)
	if create != 0o600 || final != 0o640 || !chmod {
		t.Fatalf("existing temp permissions = create %o, final %o, chmod %v", create, final, chmod)
	}
	create, final, chmod = preservingTempPermissions(false, 0, 0o666)
	if create != 0o666 || final != 0o666 || chmod {
		t.Fatalf("new temp permissions = create %o, final %o, chmod %v", create, final, chmod)
	}
}

func TestWriteFilePreservingModeReplacesRegularSymlinkWithoutChangingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "manifest")
	if err := os.WriteFile(target, []byte("target contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(target), link); err != nil {
		t.Fatal(err)
	}
	targetBefore, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFilePreservingMode(link, []byte("new manifest"), 0o666); err != nil {
		t.Fatal(err)
	}

	linkInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("replaced link mode = %v, want regular file", linkInfo.Mode())
	}
	if got := linkInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("replacement permission bits = %o, want 600", got)
	}
	targetAfter, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(targetBefore, targetAfter) {
		t.Fatal("symlink target file was replaced")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "target contents" {
		t.Fatalf("target content = %q, error = %v", data, err)
	}
	if data, err := os.ReadFile(link); err != nil || string(data) != "new manifest" {
		t.Fatalf("replacement content = %q, error = %v", data, err)
	}
}

func TestWriteFilePreservingModeReplacesDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "manifest")
	if err := os.Symlink("missing-target", link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFilePreservingMode(link, []byte("new manifest"), 0o666); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("dangling link replacement mode = %v, want regular file", info.Mode())
	}
	if data, err := os.ReadFile(link); err != nil || string(data) != "new manifest" {
		t.Fatalf("replacement content = %q, error = %v", data, err)
	}
}

func TestWriteFilePreservingModeRejectsNonregularDestinations(t *testing.T) {
	dir := t.TempDir()
	directory := filepath.Join(dir, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "socket")
	listener, err := net.Listen("unix", socket)
	paths := []string{directory, fifo}
	if err != nil {
		t.Logf("skipping socket destination check: %v", err)
	} else {
		defer listener.Close()
		paths = append(paths, socket)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteFilePreservingMode(path, []byte("replacement"), 0o666); err == nil {
				t.Fatal("nonregular destination was accepted")
			}
			after, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) {
				t.Fatal("nonregular destination was changed")
			}
		})
	}
	if data, err := os.ReadFile(filepath.Join(directory, "keep")); err != nil || string(data) != "keep" {
		t.Fatalf("directory child = %q, error = %v", data, err)
	}
}

func TestWriteFilePreservingModeHonorsRestrictiveUmask(t *testing.T) {
	const childEnv = "DBC_ATOMICFILE_UMASK_CHILD"
	if os.Getenv(childEnv) == "1" {
		path := os.Getenv("DBC_ATOMICFILE_UMASK_PATH")
		syscall.Umask(0o077)
		if err := WriteFilePreservingMode(path, []byte("new"), 0o666); err != nil {
			t.Fatal(err)
		}
		return
	}

	path := filepath.Join(t.TempDir(), "new.toml")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWriteFilePreservingModeHonorsRestrictiveUmask$")
	cmd.Env = append(os.Environ(), childEnv+"=1", "DBC_ATOMICFILE_UMASK_PATH="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("umask subprocess failed: %v\n%s", err, output)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("new file permission bits = %o, want 600 under umask 077", got)
	}
}
