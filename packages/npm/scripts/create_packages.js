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
// Requires: curl, tar, unzip (for .zip archives)
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

const GITHUB_ORG = "columnar-tech";
const GITHUB_REPO = "dbc";

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

function curlGet(url) {
  return execFileSync("curl", [
    "--silent",
    "--show-error",
    "--location",
    "--fail",
    url,
  ]);
}

function curlDownload(url, destPath) {
  execFileSync(
    "curl",
    ["--silent", "--show-error", "--location", "--fail", "-o", destPath, url],
    {
      stdio: ["ignore", "pipe", "inherit"],
    },
  );
}

function getGithubRelease(version) {
  const tag = version.startsWith("v") ? version : `v${version}`;
  const url = `https://api.github.com/repos/${GITHUB_ORG}/${GITHUB_REPO}/releases/tags/${tag}`;
  console.log(`Fetching release info for ${tag}...`);
  const body = curlGet(url);
  return JSON.parse(body.toString()).assets.map((a) => ({
    name: a.name,
    downloadUrl: a.browser_download_url,
    digest: a.digest,
  }));
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
    const assets = getGithubRelease(version);
    const platforms = opts.platform ? [opts.platform] : PLATFORMS.map((p) => p.goosArch);

    for (const goosArch of platforms) {
      const info = findPlatform(goosArch);
      if (!info) {
        console.error(`Unknown platform: ${goosArch}`);
        process.exit(1);
      }

      const [goos, goarch] = goosArch.split("-");
      const asset = assets.find(
        (a) =>
          a.name.startsWith(`dbc-${goos}-${goarch}-`) &&
          (a.name.endsWith(".tar.gz") || a.name.endsWith(".zip")),
      );

      if (!asset) {
        console.error(
          `No archive asset found for ${goosArch} in release v${version}`,
        );
        process.exit(1);
      }

      const tmpArchive = path.join(os.tmpdir(), asset.name);
      console.log(`Downloading ${asset.name}...`);
      curlDownload(asset.downloadUrl, tmpArchive);

      if (asset.digest) {
        const expected = asset.digest.split(":")[1];
        const actual = execFileSync("shasum", ["-a", "256", tmpArchive])
          .toString()
          .split(" ")[0];
        if (actual !== expected) {
          fs.unlinkSync(tmpArchive);
          throw new Error(
            `Hash mismatch for ${asset.name}: expected ${expected}, got ${actual}`,
          );
        }
        console.log(`  ✓ checksum verified`);
      }

      try {
        populatePlatform(goosArch, tmpArchive, version);
      } finally {
        fs.unlinkSync(tmpArchive);
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
