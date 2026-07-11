#!/usr/bin/env pwsh
# Scoop Go Install Script
# Usage: irm https://github.com/lihongjie0209/scoop-go/raw/master/install.ps1 | iex
# Or:    pwsh -c "irm https://github.com/lihongjie0209/scoop-go/raw/master/install.ps1 | iex"

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

# --- Config ---
$RepoOwner = "lihongjie0209"
$RepoName = "scoop-go"
$ScoopGoDir = if ($env:SCOOP) { $env:SCOOP } else { Join-Path $env:USERPROFILE "scoop" }
$ScoopGoShims = Join-Path $ScoopGoDir "shims"
$ScoopGoApps = Join-Path $ScoopGoDir "apps"
$ScoopGoBin = Join-Path $ScoopGoDir "apps\scoop-go\current\scoop-go.exe"

# --- Colors ---
function Write-HostColor($Color, $Text) { Write-Host $Text -ForegroundColor $Color }

# --- Step 1: Check Prerequisites ---
Write-HostColor Cyan "`n=== Scoop Go Installer ==="

# Check Go version for source install, or just download binary
$hasGo = $null -ne (Get-Command go -ErrorAction SilentlyContinue)

# --- Step 2: Determine install method ---
if ($args -contains "--from-source" -and $hasGo) {
    Write-HostColor Yellow "Installing from source..."
    $tempDir = Join-Path $env:TEMP "scoop-go-build"
    if (Test-Path $tempDir) { Remove-Item $tempDir -Recurse -Force }

    Write-Host "Cloning repository..."
    git clone --depth 1 "https://github.com/$RepoOwner/$RepoName.git" $tempDir 2>&1 | Out-Null

    Write-Host "Building binary..."
    Push-Location $tempDir
    go build -o scoop-go.exe ./cmd/scoop/ 2>&1 | Out-Null
    Pop-Location

    $binaryPath = Join-Path $tempDir "scoop-go.exe"
} else {
    # Download latest release from GitHub
    Write-Host "Downloading latest release..."
    $apiUrl = "https://api.github.com/repos/$RepoOwner/$RepoName/releases/latest"
    $release = Invoke-RestMethod $apiUrl
    $asset = $release.assets | Where-Object { $_.name -like "scoop-go-*-windows-amd64.exe" } | Select-Object -First 1

    if (-not $asset) {
        Write-HostColor Red "Error: No Windows binary found in latest release!"
        Write-HostColor Yellow "Falling back to source build..."
        if (-not $hasGo) {
            Write-HostColor Red "Go is required to build from source. Install Go first: https://go.dev/dl/"
            exit 1
        }
        & $PSCommandPath --from-source @args
        return
    }

    $tempFile = Join-Path $env:TEMP "scoop-go-install.exe"
    Write-Host "Downloading $($asset.name)..."
    Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $tempFile
    $binaryPath = $tempFile
}

# --- Step 3: Create Scoop directory structure ---
Write-Host "Creating directory structure..."
$appDir = Join-Path $ScoopGoApps "scoop-go"
$versionDir = Join-Path $appDir "current"
New-Item -ItemType Directory -Force -Path $versionDir | Out-Null

# Create shims directory and add to PATH
New-Item -ItemType Directory -Force -Path $ScoopGoShims | Out-Null

# Copy binary
Copy-Item $binaryPath (Join-Path $versionDir "scoop-go.exe") -Force

# --- Step 4: Add shims directory to User PATH ---
$currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($currentPath -notlike "*$ScoopGoShims*") {
    Write-Host "Adding shims directory to User PATH..."
    $newPath = "$ScoopGoShims;$currentPath"
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    # Update current session
    $env:Path = "$ScoopGoShims;$env:Path"
}

# --- Step 5: Create scoop.cmd wrapper for easy access ---
$cmdPath = Join-Path $ScoopGoShims "scoop.cmd"
@"
@echo off
"%~dp0\..\apps\scoop-go\current\scoop-go.exe" %*
"@ | Out-File $cmdPath -Encoding Ascii

# Also create scoop.ps1 for PowerShell
$ps1Path = Join-Path $ScoopGoShims "scoop.ps1"
@"
# Scoop Go shim
& "$([System.IO.Path]::GetFullPath("$PSScriptRoot\..\apps\scoop-go\current\scoop-go.exe"))" @args
exit `$LASTEXITCODE
"@ | Out-File $ps1Path -Encoding Ascii

# Also copy as scoop.exe using the scoop-go binary via shim
$shimExePath = Join-Path $ScoopGoShims "scoop.exe"
Copy-Item (Join-Path $versionDir "scoop-go.exe") $shimExePath -Force

# --- Step 6: Verify ---
Write-HostColor Cyan "`n=== Verification ==="
$testVersion = & (Join-Path $versionDir "scoop-go.exe") version 2>&1 | Out-String
if ($LASTEXITCODE -eq 0) {
    Write-HostColor Green "✅ Scoop Go installed successfully!"
    Write-Host "   $($testVersion.Trim())"
    Write-HostColor Cyan "`nQuick start:"
    Write-Host "   scoop-go bucket add main"
    Write-Host "   scoop-go install fd"
    Write-Host ""
    Write-Host "Or just use: scoop install fd"
    Write-Host ""
    Write-HostColor Yellow "Note: You may need to restart your terminal for PATH changes to take effect."
    Write-Host "      Or run: `$env:Path = [Environment]::GetEnvironmentVariable('Path','User')"
} else {
    Write-HostColor Red "❌ Installation failed!"
    exit 1
}
