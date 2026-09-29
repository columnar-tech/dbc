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

package config

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxPackageManifestSize = 1 << 20

// TODO: Unexport once we refactor sync.go. sync.go has it's own separate
// installation routine which it probably shouldn't.
func InflateTarball(f *os.File, outDir string) (Manifest, error) {
	defer f.Close()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, fmt.Errorf("could not seek to start: %w", err)
	}

	stageParent := filepath.Dir(filepath.Clean(outDir))
	stageDir, manifest, err := extractPackageArchive(f, stageParent)
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(stageDir)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("could not create output directory %s: %w", outDir, err)
	}
	root, err := os.OpenRoot(outDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not open output directory %s: %w", outDir, err)
	}
	defer root.Close()
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not read staged package: %w", err)
	}
	for _, entry := range entries {
		src := filepath.Join(stageDir, entry.Name())
		if err := copyPackageFile(src, root, entry.Name()); err != nil {
			return Manifest{}, fmt.Errorf("could not publish package file %s: %w", entry.Name(), err)
		}
	}
	return manifest, nil
}

// extractPackageArchive validates the complete archive before returning a
// private staging directory. The caller owns the returned directory.
func extractPackageArchive(f *os.File, stageParent string) (string, Manifest, error) {
	var manifest Manifest
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", manifest, fmt.Errorf("could not seek to start: %w", err)
	}
	if err := os.MkdirAll(stageParent, 0o755); err != nil {
		return "", manifest, fmt.Errorf("could not create staging parent: %w", err)
	}
	stageDir, err := os.MkdirTemp(stageParent, ".dbc-package-stage-")
	if err != nil {
		return "", manifest, fmt.Errorf("could not create private staging directory: %w", err)
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", manifest, fmt.Errorf("could not create gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	seen := make([]string, 0)
	payloads := make(map[string]struct{})
	manifestSeen := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", manifest, fmt.Errorf("error reading tarball: %w", err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if hdr.Size < 0 {
			return "", manifest, fmt.Errorf("archive entry %q has a negative size", hdr.Name)
		}
		if hdr.Typeflag == tar.TypeDir {
			return "", manifest, fmt.Errorf("found a directory entry %q which isn't supported; driver archives shouldn't contain subdirectories", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return "", manifest, fmt.Errorf("archive entry %q has unsupported type %d", hdr.Name, hdr.Typeflag)
		}
		if err := validatePackageFilename(hdr.Name); err != nil {
			return "", manifest, err
		}
		for _, previous := range seen {
			if strings.EqualFold(previous, hdr.Name) {
				return "", manifest, fmt.Errorf("duplicate or case-fold-colliding archive entry %q", hdr.Name)
			}
		}
		seen = append(seen, hdr.Name)
		if hasSparsePackagePAXRecord(hdr.PAXRecords) {
			return "", manifest, fmt.Errorf("sparse archive entry %q is not supported", hdr.Name)
		}

		if hdr.Name == "MANIFEST" {
			if manifestSeen {
				return "", manifest, errors.New("archive contains duplicate MANIFEST entries")
			}
			manifestSeen = true
			if hdr.Size > maxPackageManifestSize {
				return "", manifest, fmt.Errorf("MANIFEST exceeds %d bytes", maxPackageManifestSize)
			}
			data, err := io.ReadAll(io.LimitReader(tr, maxPackageManifestSize+1))
			if err != nil {
				return "", manifest, fmt.Errorf("could not read MANIFEST: %w", err)
			}
			if int64(len(data)) != hdr.Size {
				return "", manifest, fmt.Errorf("MANIFEST size mismatch: expected %d bytes, read %d", hdr.Size, len(data))
			}
			manifest, err = decodeManifest(bytes.NewReader(data), "", false)
			if err != nil {
				return "", Manifest{}, fmt.Errorf("could not decode manifest: %w", err)
			}
			continue
		}

		file, err := os.OpenFile(filepath.Join(stageDir, hdr.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
		if err != nil {
			return "", manifest, fmt.Errorf("could not create staged file %s: %w", hdr.Name, err)
		}
		written, copyErr := io.CopyN(file, tr, hdr.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return "", manifest, fmt.Errorf("could not write staged file %s: %w", hdr.Name, copyErr)
		}
		if closeErr != nil {
			return "", manifest, fmt.Errorf("could not close staged file %s: %w", hdr.Name, closeErr)
		}
		if written != hdr.Size {
			return "", manifest, fmt.Errorf("archive entry %s size mismatch: expected %d bytes, wrote %d", hdr.Name, hdr.Size, written)
		}
		payloads[hdr.Name] = struct{}{}
	}

	if _, err := io.Copy(io.Discard, gz); err != nil {
		return "", manifest, fmt.Errorf("error validating gzip stream: %w", err)
	}
	if !manifestSeen {
		return "", manifest, errors.New("archive does not contain a MANIFEST")
	}
	for _, ref := range []struct {
		kind string
		name string
	}{{"driver", manifest.Files.Driver}, {"signature", manifest.Files.Signature}} {
		if ref.name == "" {
			continue
		}
		if err := validatePackageFilename(ref.name); err != nil {
			return "", Manifest{}, fmt.Errorf("invalid Files.%s reference: %w", ref.kind, err)
		}
		if _, ok := payloads[ref.name]; !ok {
			return "", Manifest{}, fmt.Errorf("Files.%s references missing archive entry %q", ref.kind, ref.name)
		}
	}
	keepStage = true
	return stageDir, manifest, nil
}

func hasSparsePackagePAXRecord(records map[string]string) bool {
	for key := range records {
		if strings.HasPrefix(key, "GNU.sparse") {
			return true
		}
	}
	return false
}

func validatePackageFilename(name string) error {
	if name == "" || name == "." || name == ".." || filepath.IsAbs(name) ||
		strings.ContainsAny(name, `/\\`) || !utf8.ValidString(name) ||
		strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("unsafe archive filename %q", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"|?*`, r) {
			return fmt.Errorf("unsafe archive filename %q", name)
		}
	}
	base := strings.ToUpper(strings.TrimRight(strings.SplitN(name, ".", 2)[0], " ."))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$",
		"COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³":
		return fmt.Errorf("unsafe reserved archive filename %q", name)
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return fmt.Errorf("unsafe reserved archive filename %q", name)
	}
	return nil
}

func copyPackageFile(src string, root *os.Root, name string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
