# Licensed under the MIT license
# <LICENSE-MIT or https://opensource.org/licenses/MIT>, at your
# option. This file may not be copied, modified, or distributed
# except according to those terms.
#
# Based on uv https://astral.sh/uv/install.ps1

<#
.SYNOPSIS

The installer for dbc

.DESCRIPTION

This script detects the platform you're on and fetches the appropriate archive
from https://dbc.columnar.tech then unpacks the binaries and installs them to
the first of the following locations:

    $env:XDG_BIN_HOME
    $env:XDG_DATA_HOME/../bin
    $HOME/.local/bin

It will then add that dir to PATH by editing your Environment.Path registry key

.PARAMETER ArtifactDownloadUrl
The base URL where artifacts can be fetched from

.PARAMETER Help
Print help

#>

param (
    [Parameter(HelpMessage = "The base URL where artifacts can be fetched from")]
    [string]$ArtifactDownloadUrl = 'https://dbc.columnar.tech',
    [Parameter(HelpMessage = "Print Help")]
    [switch]$Help
)

$app_name = 'dbc'
$app_version = 'latest'
if ($env:DBC_INSTALLER_BASE_URL) {
    $installer_base_url = $env:DBC_INSTALLER_BASE_URL
} else {
    $installer_base_url = $ArtifactDownloadUrl
}

if ($env:DBC_DOWNLOAD_URL) {
    $ArtifactDownloadUrl = $env:DBC_DOWNLOAD_URL
} elseif ($env:INSTALLER_DOWNLOAD_URL) {
    $ArtifactDownloadUrl = $env:INSTALLER_DOWNLOAD_URL
} else {
    $ArtifactDownloadUrl = "$installer_base_url/$app_version"
}

if ($env:DBC_NO_MODIFY_PATH) {
    $NoModifyPath = $true
}

$unmanaged_install = $env:DBC_UNMANAGED_INSTALL
if ($unmanaged_install) {
    $NoModifyPath = $true
}

function Install-Binary($install_args) {
    if ($Help) {
        Get-Help $PSCommandPath -Detailed
        Exit
    }

    Initialize-Environment

    # Platform info
    $platforms = @{
        "aarch64-pc-windows-gnu" = @{
            "artifact_name" = "dbc-windows-arm64-$app_version.zip"
            "bins" = @("dbc.exe")
            "zip_ext" = ".zip"
        }
        "aarch64-pc-windows-msvc" = @{
            "artifact_name" = "dbc-windows-arm64-$app_version.zip"
            "bins" = @("dbc.exe")
            "zip_ext" = ".zip"
        }
        "i686-pc-windows-gnu" = @{
            "artifact_name" = "dbc-windows-x86-$app_version.zip"
            "bins" = @("dbc.exe")
            "zip_ext" = ".zip"
        }
        "i686-pc-windows-msvc" = @{
            "artifact_name" = "dbc-windows-x86-$app_version.zip"
            "bins" = @("dbc.exe")
            "zip_ext" = ".zip"
        }
        "x86_64-pc-windows-gnu" = @{
            "artifact_name" = "dbc-windows-amd64-$app_version.zip"
            "bins" = @("dbc.exe")
            "zip_ext" = ".zip"
        }
        "x86_64-pc-windows-msvc" = @{
            "artifact_name" = "dbc-windows-amd64-$app_version.zip"
            "bins" = @("dbc.exe")
            "zip_ext" = ".zip"
        }
    }

    $fetched = Download "$ArtifactDownloadUrl" $platforms
    # TODO: add a flag to let the user avoid this step
    try {
        Invoke-Installer -artifacts $fetched -platforms $platforms "$install_args"
    } catch {
        throw @"
We encountered an error trying to perform the installation;
please review the error messages below.

$_
"@
    }
}

function Get-TargetTriple($platforms) {
    $double = Get-Arch
    if ($platforms.Contains("$double-msvc")) {
        return "$double-msvc"
    } else {
        return "$double-gnu"
    }
}

function Get-Arch() {
    try {
        # NOTE: this might return X64 on ARM64 Windows, which is OK since emulation is available.
        # It works correctly starting in PowerShell Core 7.3 and Windows PowerShell in Win 11 22H2.
        # Ideally this would just be
        #   [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
        # but that gets a type from the wrong assembly on Windows PowerShell (i.e. not Core)
        $a = [System.Reflection.Assembly]::LoadWithPartialName("System.Runtime.InteropServices.RuntimeInformation")
        $t = $a.GetType("System.Runtime.InteropServices.RuntimeInformation")
        $p = $t.GetProperty("OSArchitecture")
        # Possible OSArchitecture Values: https://learn.microsoft.com/dotnet/api/system.runtime.interopservices.architecture
        # Rust supported platforms: https://doc.rust-lang.org/stable/rustc/platform-support.html
        switch ($p.GetValue($null).ToString())
        {
            "X86" { return "i686-pc-windows" }
            "X64" { return "x86_64-pc-windows" }
            "Arm" { return "thumbv7a-pc-windows" }
            "Arm64" { return "aarch64-pc-windows" }
        }
    } catch {
        # The above was added in .NET 4.7.1, so Windows PowerShell in versions of Windows
        # prior to Windows 10 v1709 may not have this API.
        Write-Verbose "Get-TargetTriple: Exception when trying to determine OS architecture."
        Write-Verbose $_
    }

    # This is available in .NET 4.0. We already checked for PS 5, which requires .NET 4.5.
    Write-Verbose("Get-TargetTriple: falling back to Is64BitOperatingSystem.")
    if ([System.Environment]::Is64BitOperatingSystem) {
        return "x86_64-pc-windows"
    } else {
        return "i686-pc-windows"
    }
}

function Download($download_url, $platforms) {
    $arch = Get-TargetTriple $platforms

    if (-not $platforms.ContainsKey($arch)) {
        $platforms_json = ConvertTo-Json $platforms
        throw "ERROR: could not find binaries for this platform. Last platform tried: $arch platform info: $platforms_json"
    }

    # Lookup what we expect this platform to look like
    $info = $platforms[$arch]
    $zip_ext = $info["zip_ext"]
    $bin_names = $info["bins"]
    $artifact_name = $info["artifact_name"]

    # Make a new temp dir to unpack things to
    $tmp = New-Temp-Dir
    $dir_path = "$tmp\$app_name$zip_ext"

    # Download and unpack!
    $url = "$download_url/$artifact_name"
    Write-Information "Downloading $app_name $app_version ($arch)"
    Write-Verbose "  from $url"
    Write-Verbose "  to $dir_path"
    $wc = New-Object Net.Webclient
    if ($auth_token) {
        $wc.Headers["Authorization"] = "Bearer $auth_token"
    }
    try {
        $wc.DownloadFile($url, $dir_path)
    } catch [System.Net.WebException] {
        $statusCode = [int]$_.Exception.Response.StatusCode
        if ($statusCode -eq 403) {
            throw "Error: dbc is not available for your platform: ($arch). Please contact support@columnar.tech for assistance."
        }
        throw "Error: Failed to download $url. HTTP Status Code: $statusCode"
    }    

    Write-Verbose "Unpacking to $tmp"

    # Select the tool to unpack the files with.
    #
    # As of windows 10(?), powershell comes with tar preinstalled, but in practice
    # it only seems to support .tar.gz, and not xz/zstd. Still, we should try to
    # forward all tars to it in case the user has a machine that can handle it!
    switch -Wildcard ($zip_ext) {
        ".zip" {
            Expand-Archive -Path $dir_path -DestinationPath "$tmp";
            Break
        }
        ".tar.*" {
            tar xf $dir_path -C "$tmp";
            Break
        }
        Default {
            throw "ERROR: unknown archive format $zip_ext"
        }
    }

      # Let the next step know what to copy
    $bin_paths = @()
    foreach ($bin_name in $bin_names) {
        Write-Verbose "  Unpacked $bin_name"
        $bin_paths += "$tmp\$bin_name"
    }

    return @{
        "bin_paths" = $bin_paths
        "tempdir" = $tmp
    }
}

function Invoke-Installer($artifacts, $platforms) {
    $arch = Get-TargetTriple $platforms

    if (-not $platforms.ContainsKey($arch)) {
        $platforms_json = ConvertTo-Json $platforms
        throw "ERROR: could not find binaries for this platform. Last platform tried: $arch platform info: $platforms_json"
    }

    $info = $platforms[$arch]

    $force_install_dir = $null
    $install_layout = "unspecified"
    if (($env:DBC_INSTALL_DIR)) {
        $force_install_dir = $env:DBC_INSTALL_DIR
        $install_layout = "flat"
    } elseif ($unmanaged_install) {
        $force_install_dir = $unmanaged_install
        $install_layout = "flat"
    }

    # The actual path we're going to install to
    $dest_dir = $null

    # Before actually consulting the configured install strategy, see
    # if we're overriding it.
    if (($force_install_dir)) {
        switch ($install_layout) {
            "hierarchical" {
                $dest_dir = Join-Path $force_install_dir "bin"
            }
            "flat" {
                $dest_dir = $force_install_dir
            }
            Default {
                throw "Error: unrecognized installation layout: $install_layout"
            }
        }
    }
    if (-not $dest_dir) {
        # install to $env:XDG_BIN_HOME
        $dest_dir = if (($base_dir = $env:XDG_BIN_HOME)) {
            Join-Path $base_dir ""
        }
        $install_layout = "flat"
    }
    if (-not $dest_dir) {
        # install to $env:XDG_DATA_HOME/../bin
        $dest_dir = if (($base_dir = $env:XDG_DATA_HOME)) {
            Join-Path $base_dir "../bin"
        }
        $install_layout = "flat"
    }
    if (-not $dest_dir) {
        # install to $HOME/.local/bin
        $dest_dir = if (($base_dir = $HOME)) {
            Join-Path $base_dir ".local/bin"
        }
        $install_layout = "flat"
    }

    # all of the above failed?
    if (-not $dest_dir) {
        throw "ERROR: could not find a valid path to install to; please check installation instructions"
    }

    $dest_dir = New-Item -Force -ItemType Directory -Path $dest_dir
    Write-Information "Installing to $dest_dir"
    # just copy the binaries from temp location to install dir
    foreach ($bin_path in $artifacts["bin_paths"]) {
        $installed_file = Split-Path -Path "$bin_path" -Leaf
        Copy-Item -Path "$bin_path" -Destination "$dest_dir" -ErrorAction Stop
        Remove-Item "$bin_path" -Recurse -Force -ErrorAction Stop
        Write-Information "  + $installed_file"
    }

    Remove-Item -Path $artifacts["tempdir"] -Recurse -Force -ErrorAction SilentlyContinue

    # Respect the environment, but CLI takes precedence
    if ($null -eq $NoModifyPath) {
        $NoModifyPath = $env:INSTALLER_NO_MODIFY_PATH
    }

    Write-Information "Successfully installed dbc!"
    if (-not $NoModifyPath) {
        Add-Ci-Path $dest_dir
        if (Add-Path $dest_dir) {
            Write-Information ""
            Write-Information "To add $dest_dir to your PATH, either restart your shell or run:"
            Write-Information ""
            Write-Information "    set Path=$dest_dir;%Path%   (cmd)"
            Write-Information "    `$env:Path = `"$dest_dir;`$env:Path`"   (powershell)"
        }
    }
}

# Attempt to do CI-specific rituals to get the install-dir on PATH faster
function Add-Ci-Path($OrigPathToAdd) {
  # If GITHUB_PATH is present, then write install_dir to the file it refs.
  # After each GitHub Action, the contents will be added to PATH.
  # So if you put a curl | sh for this script in its own "run" step,
  # the next step will have this dir on PATH.
  #
  # Note that GITHUB_PATH will not resolve any variables, so we in fact
  # want to write the install dir and not an expression that evals to it
  if (($gh_path = $env:GITHUB_PATH)) {
    Write-Output "$OrigPathToAdd" | Out-File -FilePath "$gh_path" -Encoding utf8 -Append
  }
}


# Try to permanently add the given path to the user-level
# PATH via the registry
#
# Returns true if the registry was modified, otherwise returns false
# (indicating it was already on PATH)
#
# This is a lightly modified version of this solution:
# https://stackoverflow.com/questions/69236623/adding-path-permanently-to-windows-using-powershell-doesnt-appear-to-work/69239861#69239861
function Add-Path($LiteralPath) {
  Write-Verbose "Adding $LiteralPath to your user-level PATH"

  $RegistryPath = 'registry::HKEY_CURRENT_USER\Environment'

  # Note the use of the .GetValue() method to ensure that the *unexpanded* value is returned.
  # If 'Path' is not an existing item in the registry, '' is returned.
  $CurrentDirectories = (Get-Item -LiteralPath $RegistryPath).GetValue('Path', '', 'DoNotExpandEnvironmentNames') -split ';' -ne ''

  if ($LiteralPath -in $CurrentDirectories) {
    Write-Verbose "Install directory $LiteralPath already on PATH, all done!"
    return $false
  }

  Write-Verbose "Actually mutating 'Path' Property"

  # Add the new path to the front of the PATH.
  # The ',' turns $LiteralPath into an array, which the array of
  # $CurrentDirectories is then added to.
  $NewPath = (,$LiteralPath + $CurrentDirectories) -join ';'

  # Update the registry. Will create the property if it did not already exist.
  # Note the use of ExpandString to create a registry property with a REG_EXPAND_SZ data type.
  Set-ItemProperty -Type ExpandString -LiteralPath $RegistryPath Path $NewPath

  # Broadcast WM_SETTINGCHANGE to get the Windows shell to reload the
  # updated environment, via a dummy [Environment]::SetEnvironmentVariable() operation.
  $DummyName = 'dbc-dist-' + [guid]::NewGuid().ToString()
  [Environment]::SetEnvironmentVariable($DummyName, 'dbc-dist-dummy', 'User')
  [Environment]::SetEnvironmentVariable($DummyName, [NullString]::value, 'User')

  Write-Verbose "Successfully added $LiteralPath to your user-level PATH"
  return $true
}

function Initialize-Environment() {
  If (($PSVersionTable.PSVersion.Major) -lt 5) {
    throw @"
Error: PowerShell 5 or later is required to install $app_name.
Upgrade PowerShell:

    https://docs.microsoft.com/en-us/powershell/scripting/setup/installing-windows-powershell

"@
  }

  # show notification to change execution policy:
  $allowedExecutionPolicy = @('Unrestricted', 'RemoteSigned', 'Bypass')
  If ((Get-ExecutionPolicy).ToString() -notin $allowedExecutionPolicy) {
    throw @"
Error: PowerShell requires an execution policy in [$($allowedExecutionPolicy -join ", ")] to run $app_name. For example, to set the execution policy to 'RemoteSigned' please run:

    Set-ExecutionPolicy RemoteSigned -scope CurrentUser

"@
  }

  # GitHub requires TLS 1.2
  If ([System.Enum]::GetNames([System.Net.SecurityProtocolType]) -notcontains 'Tls12') {
    throw @"
Error: Installing $app_name requires at least .NET Framework 4.5
Please download and install it first:

    https://www.microsoft.com/net/download

"@
  }
}


function New-Temp-Dir() {
  [CmdletBinding(SupportsShouldProcess)]
  param()
  $parent = [System.IO.Path]::GetTempPath()
  [string] $name = [System.Guid]::NewGuid()
  New-Item -ItemType Directory -Path (Join-Path $parent $name)
}

# PSScriptAnalyzer doesn't like how we use our params as globals, this calms it
$Null = $ArtifactDownloadUrl, $NoModifyPath, $Help
# Make Write-Information statements be visible
$InformationPreference = "Continue"

# The default interactive handler
try {
  Install-Binary "$Args"
} catch {
  Write-Information $_
  exit 1
}
