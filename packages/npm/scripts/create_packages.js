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

const PACKAGES_DIR  = path.resolve(__dirname, "..", "packages");
const WRAPPER_DIR   = path.resolve(__dirname, "..", "wrapper");
const REPO_ROOT     = path.resolve(__dirname, "..", "..", "..");
const PLATFORM_README_TEMPLATE = path.join(PACKAGES_DIR, "README.template.md");

function ghDownload(tag, pattern, destDir) {
  execFileSync(
    "gh",
    [
      "release",
      "download",
      tag,
      "--repo",
      GITHUB_REPO,
      "--pattern",
      pattern,
      "--dir",
      destDir,
      "--clobber",
    ],
    { stdio: "inherit" },
  );
}

// Return the asset names for a release tag.
function ghReleaseAssets(tag) {
  const out = execFileSync("gh", [
    "release",
    "view",
    tag,
    "--repo",
    GITHUB_REPO,
    "--json",
    "assets",
  ]);
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

const COMMON_FIELDS = {
  keywords: ["adbc", "arrow", "database", "drivers", "dbc"],
  homepage: "https://columnar.tech/dbc",
  bugs: "https://github.com/columnar-tech/dbc/issues",
  license: "Apache-2.0",
};

function writePlatformPackageJson(info, pkgDir, version) {
  const unscoped = info.npmPkg.replace(/^@[^/]+\//, ""); // e.g. "dbc-darwin-arm64"
  const pkgJson = {
    name: info.npmPkg,
    version,
    description: info.description,
    ...COMMON_FIELDS,
    repository: {
      type: "git",
      url: "https://github.com/columnar-tech/dbc.git",
      directory: `packages/npm/packages/${unscoped}`,
    },
    os: [info.os],
    cpu: [info.cpu],
    files: ["bin/", "README.md", "LICENSE"],
  };
  fs.writeFileSync(
    path.join(pkgDir, "package.json"),
    JSON.stringify(pkgJson, null, 2) + "\n",
  );
}

function writeWrapperPackageJson(version) {
  const optionalDependencies = Object.fromEntries(
    PLATFORMS.map((p) => [p.npmPkg, version]),
  );
  const pkgJson = {
    name: "@columnar-tech/dbc",
    version,
    description: "The CLI for installing and managing ADBC drivers",
    ...COMMON_FIELDS,
    repository: {
      type: "git",
      url: "https://github.com/columnar-tech/dbc.git",
      directory: "packages/npm/wrapper",
    },
    bin: { dbc: "bin/dbc.js" },
    files: ["bin/", "README.md", "LICENSE"],
    optionalDependencies,
  };
  fs.writeFileSync(
    path.join(WRAPPER_DIR, "package.json"),
    JSON.stringify(pkgJson, null, 2) + "\n",
  );
  fs.copyFileSync(path.join(REPO_ROOT, "LICENSE"), path.join(WRAPPER_DIR, "LICENSE"));
  console.log(`Wrote wrapper package.json at version ${version}`);
}

function writePlatformDocs(info, pkgDir) {
  // README: substitute PLATFORM placeholder with the unscoped package name
  const unscoped = info.npmPkg.replace(/^@[^/]+\/dbc-/, ""); // e.g. "darwin-arm64"
  const readme = fs.readFileSync(PLATFORM_README_TEMPLATE, "utf8")
    .replaceAll("PLATFORM_SUFFIX", unscoped);
  fs.writeFileSync(path.join(pkgDir, "README.md"), readme);

  // LICENSE: copy from the repo root
  fs.copyFileSync(path.join(REPO_ROOT, "LICENSE"), path.join(pkgDir, "LICENSE"));
}

function populatePlatform(goosArch, archivePath, version) {
  const info = findPlatform(goosArch);
  if (!info) throw new Error(`Unknown platform: ${goosArch}`);

  const pkgDir = pkgDirFor(info);
  const binDir = path.join(pkgDir, "bin");
  fs.mkdirSync(binDir, { recursive: true });
  const destPath = path.join(binDir, info.binary);

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

  writePlatformDocs(info, pkgDir);
  writePlatformPackageJson(info, pkgDir, version);
  console.log(`  → wrote ${info.npmPkg} package.json at version ${version}`);
}

function parseArgs() {
  const args = process.argv.slice(2);
  const opts = {
    version: null,
    platform: null,
    archive: null,
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

  if (opts.archive) {
    if (!opts.platform) {
      console.error("--platform is required when --archive is provided");
      process.exit(1);
    }
    populatePlatform(opts.platform, path.resolve(opts.archive), version);
    writeWrapperPackageJson(version);
    created.push(pkgDirFor(findPlatform(opts.platform)));
    created.push(WRAPPER_DIR);
  } else {
    // Download from GitHub releases
    const tag = `v${version}`;
    const platforms = opts.platform
      ? [opts.platform]
      : PLATFORMS.map((p) => p.goosArch);

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
        console.error(
          `No archive asset found for ${goosArch} in release ${tag}`,
        );
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

    writeWrapperPackageJson(version);
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
