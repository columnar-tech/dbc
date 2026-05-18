#!/usr/bin/env node
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

// create_packages.js
//
// Populates the per-platform npm packages with the dbc binary extracted from
// a GoReleaser archive. Mirrors scripts/create_wheels.py in design.
//
// Requires: gh, tar, unzip (for .zip archives)
//
// Usage:
//
//   # Create npm packages for all platforms in one go:
//   node create_packages.js --version 0.3.0
//
//   # Create npm packages for a specific platform, including the wrapper:
//   node create_packages.js --version 0.3.0 --platform darwin-arm64

"use strict";

const fs = require("fs");
const os = require("os");
const path = require("path");
const { execFileSync } = require("child_process");

const GITHUB_REPO = "columnar-tech/dbc";

const { PLATFORMS } = require("../platforms.js");

function findPlatform(goosArch) {
  return PLATFORMS.find((p) => p.goosArch === goosArch);
}

// The platform packages live in packages/<dir> where <dir> is the unscoped
// package name (e.g. "dbc-darwin-arm64", not "@columnar-tech/dbc-darwin-arm64")
function pkgDirFor(info) {
  return path.join(PACKAGES_DIR, info.npmPkg.replace(/^@[^/]+\//, ""));
}

const PACKAGES_DIR = path.resolve(__dirname, "..", "packages");
const WRAPPER_DIR = path.resolve(__dirname, "..", "wrapper");

// Download a release asset by name pattern into destDir, with checksum
// verification handled by gh itself.
function ghDownload(tag, pattern, destDir) {
  execFileSync(
    "gh", ["release", "download", tag,
      "--repo", GITHUB_REPO,
      "--pattern", pattern,
      "--dir", destDir,
      "--clobber",
    ],
    { stdio: "inherit" },
  );
}

// Return the asset names for a release tag.
function ghReleaseAssets(tag) {
  const out = execFileSync(
    "gh", ["release", "view", tag,
      "--repo", GITHUB_REPO,
      "--json", "assets",
    ],
  );
  return JSON.parse(out.toString()).assets.map((a) => a.name);
}

function extractBinary(archivePath, binaryName, destDir) {
  if (archivePath.endsWith(".zip")) {
    // -j: junk paths (strip directory components)
    execFileSync(
      "unzip",
      ["-j", "-o", archivePath, `*${binaryName}`, "-d", destDir],
      {
        stdio: ["ignore", "pipe", "pipe"],
      },
    );
  } else {
    // GoReleaser archives are flat (no top-level directory wrapper)
    execFileSync("tar", ["-xzf", archivePath, "-C", destDir], {
      stdio: ["ignore", "pipe", "pipe"],
    });
  }

  const extracted = path.join(destDir, binaryName);
  if (!fs.existsSync(extracted)) {
    throw new Error(
      `Binary '${binaryName}' not found after extracting ${archivePath}`,
    );
  }
  return extracted;
}

function setPackageVersion(pkgDir, version) {
  const pkgJsonPath = path.join(pkgDir, "package.json");
  const pkgJson = JSON.parse(fs.readFileSync(pkgJsonPath, "utf8"));
  pkgJson.version = version;
  fs.writeFileSync(pkgJsonPath, JSON.stringify(pkgJson, null, 2) + "\n");
}

function setWrapperVersion(version) {
  const pkgJsonPath = path.join(WRAPPER_DIR, "package.json");
  const pkgJson = JSON.parse(fs.readFileSync(pkgJsonPath, "utf8"));
  pkgJson.version = version;
  for (const dep of Object.keys(pkgJson.optionalDependencies)) {
    pkgJson.optionalDependencies[dep] = version;
  }
  fs.writeFileSync(pkgJsonPath, JSON.stringify(pkgJson, null, 2) + "\n");
  console.log(`Updated wrapper package.json to version ${version}`);
}

function populatePlatform(goosArch, archivePath, version) {
  const info = findPlatform(goosArch);
  if (!info) throw new Error(`Unknown platform: ${goosArch}`);

  const pkgDir = pkgDirFor(info);
  const destPath = path.join(pkgDir, "bin", info.binary);

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "dbc-npm-"));
  try {
    console.log(
      `Extracting ${info.binary} from ${path.basename(archivePath)}...`,
    );
    const extracted = extractBinary(archivePath, info.binary, tmpDir);
    fs.copyFileSync(extracted, destPath);
    fs.chmodSync(destPath, 0o755);
    console.log(`  → ${destPath} (${fs.statSync(destPath).size} bytes)`);
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  setPackageVersion(pkgDir, version);
  console.log(`  → set ${info.npmPkg} version to ${version}`);
}

function parseArgs() {
  const args = process.argv.slice(2);
  const opts = {
    version: null,
    platform: null,
    archive: null,
    setVersionOnly: false,
  };

  for (let i = 0; i < args.length; i++) {
    switch (args[i]) {
      case "--version":
        opts.version = args[++i];
        break;
      case "--platform":
        opts.platform = args[++i];
        break;
      case "--archive":
        opts.archive = args[++i];
        break;
      case "--set-version-only":
        opts.setVersionOnly = true;
        break;
      default:
        console.error(`Unknown argument: ${args[i]}`);
        process.exit(1);
    }
  }

  if (!opts.version) {
    console.error("--version is required");
    process.exit(1);
  }

  return opts;
}

function main() {
  const opts = parseArgs();
  const version = opts.version.replace(/^v/, "");

  const created = [];

  if (opts.setVersionOnly) {
    const platforms = opts.platform ? [opts.platform] : PLATFORMS.map((p) => p.goosArch);
    for (const goosArch of platforms) {
      const info = findPlatform(goosArch);
      if (!info) {
        console.error(`Unknown platform: ${goosArch}`);
        process.exit(1);
      }
      const pkgDir = pkgDirFor(info);
      setPackageVersion(pkgDir, version);
      created.push(pkgDir);
    }
    setWrapperVersion(version);
    created.push(WRAPPER_DIR);
  } else if (opts.archive) {
    if (!opts.platform) {
      console.error("--platform is required when --archive is provided");
      process.exit(1);
    }
    populatePlatform(opts.platform, path.resolve(opts.archive), version);
    setWrapperVersion(version);
    created.push(pkgDirFor(findPlatform(opts.platform)));
    created.push(WRAPPER_DIR);
  } else {
    // Download from GitHub releases
    const tag = `v${version}`;
    const platforms = opts.platform ? [opts.platform] : PLATFORMS.map((p) => p.goosArch);

    // Find the actual archive filename for each platform from the release
    const assetNames = ghReleaseAssets(tag);

    for (const goosArch of platforms) {
      const info = findPlatform(goosArch);
      if (!info) {
        console.error(`Unknown platform: ${goosArch}`);
        process.exit(1);
      }

      const [goos, goarch] = goosArch.split("-");
      const assetName = assetNames.find(
        (n) =>
          n.startsWith(`dbc-${goos}-${goarch}-`) &&
          (n.endsWith(".tar.gz") || n.endsWith(".zip")),
      );

      if (!assetName) {
        console.error(`No archive asset found for ${goosArch} in release ${tag}`);
        process.exit(1);
      }

      const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "dbc-npm-dl-"));
      try {
        // gh verifies checksums automatically against the release's checksums file
        console.log(`Downloading ${assetName}...`);
        ghDownload(tag, assetName, tmpDir);
        populatePlatform(goosArch, path.join(tmpDir, assetName), version);
      } finally {
        fs.rmSync(tmpDir, { recursive: true, force: true });
      }

      created.push(pkgDirFor(info));
    }

    setWrapperVersion(version);
    created.push(WRAPPER_DIR);
  }

  console.log(`\nCreated ${created.length} package(s):`);
  for (const p of created) {
    console.log(`  ${p}`);
  }
}

try {
  main();
} catch (err) {
  console.error(err.message);
  process.exit(1);
}
