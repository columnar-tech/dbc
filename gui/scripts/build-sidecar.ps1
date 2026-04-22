# Copyright 2026 Columnar Technologies Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

param(
    [string]$Target = ""
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent (Split-Path -Parent $ScriptDir)
$BinariesDir = Join-Path $ScriptDir "..\src-tauri\binaries"

if (-not $Target) {
    $Target = $env:TAURI_ENV_TARGET_TRIPLE
}
if (-not $Target) {
    Write-Error "Target triple required. Pass -Target or set TAURI_ENV_TARGET_TRIPLE."
    exit 1
}

$mapping = @{
    "x86_64-apple-darwin"       = @{ GOOS="darwin";  GOARCH="amd64"; Ext="" }
    "aarch64-apple-darwin"      = @{ GOOS="darwin";  GOARCH="arm64"; Ext="" }
    "x86_64-pc-windows-msvc"    = @{ GOOS="windows"; GOARCH="amd64"; Ext=".exe" }
    "x86_64-unknown-linux-gnu"  = @{ GOOS="linux";   GOARCH="amd64"; Ext="" }
    "aarch64-unknown-linux-gnu" = @{ GOOS="linux";   GOARCH="arm64"; Ext="" }
}

if (-not $mapping.ContainsKey($Target)) {
    Write-Error "Unsupported target: $Target"
    exit 1
}

$m = $mapping[$Target]
$Output = Join-Path $BinariesDir "dbc-${Target}$($m.Ext)"
New-Item -ItemType Directory -Force -Path $BinariesDir | Out-Null

$env:CGO_ENABLED = "0"
$env:GOOS = $m.GOOS
$env:GOARCH = $m.GOARCH

$Version = if ($env:VERSION) { $env:VERSION } else { "dev" }

Write-Host "Building dbc sidecar for $Target..."
& go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $Output "$RepoRoot\cmd\dbc"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Built: $Output"
