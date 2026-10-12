<#
.SYNOPSIS
  ccswitch installer for Windows (PowerShell 5.1 and 7).

.DESCRIPTION
  irm https://raw.githubusercontent.com/ccswitch/ccswitch/main/install.ps1 | iex

  Environment:
    CCSWITCH_VERSION   Version to install, for example v0.1.0. Default: latest release.

  Downloads the zip and checksums.txt from GitHub Releases, verifies SHA-256,
  installs to $env:LOCALAPPDATA\Programs\ccswitch and adds that folder to your
  user PATH if it is missing.
#>

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Repo = 'ccswitch/ccswitch'
$Bin = 'ccswitch.exe'

try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch { }

function Fail([string]$Message) {
  Write-Host "error: $Message" -ForegroundColor Red
  throw $Message
}

# Detect architecture.
$archRaw = $env:PROCESSOR_ARCHITEW6432
if (-not $archRaw) { $archRaw = $env:PROCESSOR_ARCHITECTURE }
switch ($archRaw) {
  'AMD64' { $Arch = 'amd64' }
  'ARM64' { $Arch = 'arm64' }
  default { Fail "Unsupported CPU architecture: $archRaw. ccswitch supports amd64 and arm64." }
}

# Resolve version.
$Version = $env:CCSWITCH_VERSION
if (-not $Version) {
  Write-Host 'Looking up the latest release...'
  try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ 'User-Agent' = 'ccswitch-installer' }
    $Version = $release.tag_name
  } catch {
    Fail 'Could not reach the GitHub API. Set $env:CCSWITCH_VERSION (for example v0.1.0) and retry.'
  }
}
if (-not $Version) { Fail 'Could not determine the version to install.' }
if ($Version.StartsWith('v')) { $Tag = $Version } else { $Tag = "v$Version" }
$Num = $Tag.Substring(1)

$Archive = "ccswitch_${Num}_windows_${Arch}.zip"
$BaseUrl = "https://github.com/$Repo/releases/download/$Tag"

$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("ccswitch-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $Tmp | Out-Null

try {
  Write-Host "Downloading $Archive ($Tag)..."
  try {
    Invoke-WebRequest -Uri "$BaseUrl/$Archive" -OutFile (Join-Path $Tmp $Archive) -UseBasicParsing
    Invoke-WebRequest -Uri "$BaseUrl/checksums.txt" -OutFile (Join-Path $Tmp 'checksums.txt') -UseBasicParsing
  } catch {
    Fail "Download failed from $BaseUrl. Check the version and your network."
  }

  Write-Host 'Verifying SHA-256...'
  $want = $null
  foreach ($line in Get-Content (Join-Path $Tmp 'checksums.txt')) {
    $parts = $line -split '\s+', 2
    if ($parts.Count -eq 2 -and $parts[1].TrimStart('*').Trim() -eq $Archive) { $want = $parts[0].ToLower(); break }
  }
  if (-not $want) { Fail "$Archive is not listed in checksums.txt" }
  $got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $Tmp $Archive)).Hash.ToLower()
  if ($want -ne $got) {
    Write-Host "expected: $want" -ForegroundColor Red
    Write-Host "actual:   $got" -ForegroundColor Red
    Fail "Checksum mismatch for $Archive. Refusing to install. Nothing was changed."
  }

  Write-Host 'Extracting...'
  $extract = Join-Path $Tmp 'x'
  Expand-Archive -Path (Join-Path $Tmp $Archive) -DestinationPath $extract -Force
  $exe = Join-Path $extract $Bin
  if (-not (Test-Path $exe)) { Fail "$Bin not found in the archive." }

  $Dest = Join-Path $env:LOCALAPPDATA 'Programs\ccswitch'
  New-Item -ItemType Directory -Path $Dest -Force | Out-Null
  Write-Host "Installing to $Dest\$Bin..."
  try {
    Copy-Item -Path $exe -Destination (Join-Path $Dest $Bin) -Force
  } catch {
    Fail "Could not write $Dest\$Bin. Close any running ccswitch and retry."
  }

  # Add to user PATH if missing.
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (-not $userPath) { $userPath = '' }
  $entries = $userPath -split ';' | Where-Object { $_ -ne '' }
  $onPath = $false
  foreach ($e in $entries) { if ($e.TrimEnd('\') -ieq $Dest.TrimEnd('\')) { $onPath = $true } }
  if (-not $onPath) {
    $newPath = (@($entries) + $Dest) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path = "$env:Path;$Dest"
    Write-Host "Added $Dest to your user PATH. Open a new terminal for it to take effect everywhere."
  }

  Write-Host ''
  & (Join-Path $Dest $Bin) version
  Write-Host ''
  Write-Host "Done. Run 'ccswitch' to start, or 'ccswitch doctor' to check your setup."
}
finally {
  Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}
