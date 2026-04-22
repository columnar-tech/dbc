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
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

func main() {
	// runtime.Caller(0) gives us this file's compile-time path; parent is
	// the fixture-registry root, so fixtures/ is always found correctly.
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filename))
	driversDir := filepath.Join(root, "fixtures", "drivers")

	platforms := []string{"macos_arm64", "macos_amd64", "linux_amd64", "windows_amd64"}
	drivers := []struct{ path, label string }{
		{"fixture-happy", "happy"},
		{"fixture-tampered", "tampered"},
	}

	for _, d := range drivers {
		for _, platform := range platforms {
			outDir := filepath.Join(driversDir, d.path, "1.0.0")
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", outDir, err)
				os.Exit(1)
			}

			tarName := fmt.Sprintf("%s_%s-1.0.0.tar.gz", d.path, platform)
			tarPath := filepath.Join(outDir, tarName)

			checksum, err := writeTarball(tarPath, d.label, platform)
			if err != nil {
				fmt.Fprintf(os.Stderr, "create %s: %v\n", tarPath, err)
				os.Exit(1)
			}
			fmt.Printf("wrote %s  sha256:%s\n", tarPath, checksum)
		}
	}
}

func writeTarball(path, driverLabel, platform string) (string, error) {
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	w := io.MultiWriter(f, h)

	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	content := []byte(fmt.Sprintf("fixture driver=%s platform=%s\n", driverLabel, platform))
	if err := tw.WriteHeader(&tar.Header{Name: "README.txt", Mode: 0o644, Size: int64(len(content))}); err != nil {
		return "", err
	}
	if _, err := tw.Write(content); err != nil {
		return "", err
	}

	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gw.Close(); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
