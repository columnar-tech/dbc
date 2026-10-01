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

	"github.com/columnar-tech/dbc/internal/hostpath"
)

var errPackageArchiveByteLimit = errors.New("package archive exceeds its decompressed byte limit")

const maxPackageManifestSize = 1 << 20

type packageArchiveLimits struct {
	manifestSize int64
	entrySize    int64
	totalSize    int64
	entryCount   int
	metadataSize int64
}

// packageArchiveByteBudget allows limit bytes through and probes one extra byte
// to distinguish exact EOF from an over-limit stream.
type packageArchiveByteBudget struct {
	reader   io.Reader
	limit    int64
	read     int64
	exceeded bool
}

func (b *packageArchiveByteBudget) extend(n int64) error {
	if n < 0 || n > int64(^uint64(0)>>1)-b.limit {
		return errors.New("package archive byte budget overflow")
	}
	b.limit += n
	return nil
}

func (b *packageArchiveByteBudget) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.exceeded {
		return 0, errPackageArchiveByteLimit
	}
	remaining := b.limit - b.read
	if remaining <= 0 {
		var probe [1]byte
		n, err := b.reader.Read(probe[:])
		if n > 0 {
			b.exceeded = true
			return 0, errPackageArchiveByteLimit
		}
		return 0, err
	}
	if int64(len(p)) > remaining {
		p = p[:int(remaining)]
	}
	n, err := b.reader.Read(p)
	b.read += int64(n)
	return n, err
}

var defaultPackageArchiveLimits = packageArchiveLimits{
	manifestSize: maxPackageManifestSize,
	entrySize:    2 << 30,
	totalSize:    4 << 30,
	entryCount:   4096,
	metadataSize: 16 << 20,
}

func InflateTarball(f *os.File, outDir string) (Manifest, error) {
	return inflateTarballWithLimits(f, outDir, defaultPackageArchiveLimits)
}

func inflateTarballWithLimits(f *os.File, outDir string, limits packageArchiveLimits) (Manifest, error) {
	defer f.Close()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, fmt.Errorf("could not seek to start: %w", err)
	}

	stageParent := hostpath.Dir(hostpath.Clean(outDir))
	stageDir, manifest, payloadNames, err := extractPackageArchiveWithLimits(f, stageParent, limits)
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(stageDir)

	if err := hostpath.MkdirAll(outDir, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("could not create output directory %s: %w", outDir, err)
	}
	root, err := os.OpenRoot(outDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not open output directory %s: %w", outDir, err)
	}
	defer root.Close()
	for _, name := range payloadNames {
		src := hostpath.Join(stageDir, name)
		if err := copyPackageFile(src, root, name); err != nil {
			return Manifest{}, fmt.Errorf("could not publish package file %s: %w", name, err)
		}
	}
	return manifest, nil
}

// extractPackageArchive validates the complete archive before returning a
// private staging directory. The caller owns the returned directory.
func extractPackageArchive(f *os.File, stageParent string) (string, Manifest, []string, error) {
	return extractPackageArchiveWithLimits(f, stageParent, defaultPackageArchiveLimits)
}

func extractPackageArchiveWithLimits(f *os.File, stageParent string, limits packageArchiveLimits) (string, Manifest, []string, error) {
	var manifest Manifest
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", manifest, nil, fmt.Errorf("could not seek to start: %w", err)
	}
	if err := hostpath.MkdirAll(stageParent, 0o755); err != nil {
		return "", manifest, nil, fmt.Errorf("could not create staging parent: %w", err)
	}
	stageDir, err := os.MkdirTemp(stageParent, ".dbc-package-stage-")
	if err != nil {
		return "", manifest, nil, fmt.Errorf("could not create private staging directory: %w", err)
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", manifest, nil, fmt.Errorf("could not create gzip reader: %w", err)
	}
	defer gz.Close()

	budget := &packageArchiveByteBudget{reader: gz, limit: limits.metadataSize}
	tr := tar.NewReader(budget)
	seen := make([]string, 0)
	payloads := make(map[string]struct{})
	payloadNames := make([]string, 0)
	manifestSeen := false
	declaredRemaining := limits.totalSize
	entryCount := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", manifest, nil, fmt.Errorf("error reading tarball: %w", err)
		}
		entryCount++
		if entryCount > limits.entryCount {
			return "", manifest, nil, fmt.Errorf("archive exceeds the limit of %d entries", limits.entryCount)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if hdr.Size < 0 {
			return "", manifest, nil, fmt.Errorf("archive entry %q has a negative size", hdr.Name)
		}
		if hdr.Typeflag == tar.TypeDir {
			return "", manifest, nil, fmt.Errorf("found a directory entry %q which isn't supported; driver archives shouldn't contain subdirectories", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return "", manifest, nil, fmt.Errorf("archive entry %q has unsupported type %d", hdr.Name, hdr.Typeflag)
		}
		if hdr.Size > limits.entrySize {
			return "", manifest, nil, fmt.Errorf("archive entry %q exceeds the limit of %d bytes", hdr.Name, limits.entrySize)
		}
		if hdr.Size > declaredRemaining {
			return "", manifest, nil, fmt.Errorf("archive exceeds the limit of %d regular file bytes", limits.totalSize)
		}
		if err := validatePackageFilename(hdr.Name); err != nil {
			return "", manifest, nil, err
		}
		if strings.EqualFold(hdr.Name, packageInstallReceiptFilename) {
			return "", manifest, nil, fmt.Errorf("archive entry %q uses a reserved package receipt filename", hdr.Name)
		}
		for _, previous := range seen {
			if strings.EqualFold(previous, hdr.Name) {
				return "", manifest, nil, fmt.Errorf("duplicate or case-fold-colliding archive entry %q", hdr.Name)
			}
		}
		seen = append(seen, hdr.Name)
		if hasSparsePackagePAXRecord(hdr.PAXRecords) {
			return "", manifest, nil, fmt.Errorf("sparse archive entry %q is not supported", hdr.Name)
		}

		if hdr.Name == "MANIFEST" {
			if manifestSeen {
				return "", manifest, nil, errors.New("archive contains duplicate MANIFEST entries")
			}
			if hdr.Size > limits.manifestSize {
				return "", manifest, nil, fmt.Errorf("MANIFEST exceeds %d bytes", limits.manifestSize)
			}
		}
		declaredRemaining -= hdr.Size
		if err := budget.extend(hdr.Size); err != nil {
			return "", manifest, nil, err
		}

		if hdr.Name == "MANIFEST" {
			manifestSeen = true
			data, err := io.ReadAll(io.LimitReader(tr, limits.manifestSize+1))
			if err != nil {
				return "", manifest, nil, fmt.Errorf("could not read MANIFEST: %w", err)
			}
			if int64(len(data)) != hdr.Size {
				return "", manifest, nil, fmt.Errorf("MANIFEST size mismatch: expected %d bytes, read %d", hdr.Size, len(data))
			}
			manifest, err = decodeManifest(bytes.NewReader(data), "", false)
			if err != nil {
				return "", Manifest{}, nil, fmt.Errorf("could not decode manifest: %w", err)
			}
			continue
		}

		file, err := os.OpenFile(hostpath.Join(stageDir, hdr.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
		if err != nil {
			return "", manifest, nil, fmt.Errorf("could not create staged file %s: %w", hdr.Name, err)
		}
		written, copyErr := io.CopyN(file, tr, hdr.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return "", manifest, nil, fmt.Errorf("could not write staged file %s: %w", hdr.Name, copyErr)
		}
		if closeErr != nil {
			return "", manifest, nil, fmt.Errorf("could not close staged file %s: %w", hdr.Name, closeErr)
		}
		if written != hdr.Size {
			return "", manifest, nil, fmt.Errorf("archive entry %s size mismatch: expected %d bytes, wrote %d", hdr.Name, hdr.Size, written)
		}
		payloads[hdr.Name] = struct{}{}
		payloadNames = append(payloadNames, hdr.Name)
	}

	if _, err := io.Copy(io.Discard, budget); err != nil {
		return "", manifest, nil, fmt.Errorf("error validating gzip stream: %w", err)
	}
	if !manifestSeen {
		return "", manifest, nil, errors.New("archive does not contain a MANIFEST")
	}
	if manifest.Files.Driver == "" {
		return "", Manifest{}, nil, errors.New("package manifest does not specify Files.driver")
	}
	for _, ref := range []struct {
		kind string
		name string
	}{{"driver", manifest.Files.Driver}, {"signature", manifest.Files.Signature}} {
		if ref.name == "" {
			continue
		}
		if err := validatePackageFilename(ref.name); err != nil {
			return "", Manifest{}, nil, fmt.Errorf("invalid Files.%s reference: %w", ref.kind, err)
		}
		if _, ok := payloads[ref.name]; !ok {
			return "", Manifest{}, nil, fmt.Errorf("Files.%s references missing archive entry %q", ref.kind, ref.name)
		}
	}
	keepStage = true
	return stageDir, manifest, payloadNames, nil
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
