#Requires -Version 5.1
param(
  [string]$Repository = 'SuInk/bgmcat',
  [string]$Version = 'latest',
  [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'AnimeRSS\bin'),
  [string]$LocalDir = '',
  [switch]$Private
)
$ErrorActionPreference = 'Stop'
if (-not [IO.Path]::IsPathRooted($InstallDir)) { throw 'InstallDir must be absolute' }
if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64') { throw 'This release supports Windows AMD64 only' }
$asset = 'bgmcat-windows-amd64.zip'
$temp = Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $temp | Out-Null
try {
  if ($LocalDir) {
    Copy-Item -LiteralPath (Join-Path $LocalDir $asset) -Destination $temp
    Copy-Item -LiteralPath (Join-Path $LocalDir 'SHA256SUMS') -Destination $temp
  } else {
    if ($Repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') { throw 'Specify -Repository OWNER/REPO' }
    if ($Version -ne 'latest' -and $Version -notmatch '^v\d+\.\d+\.\d+(-[A-Za-z0-9.-]+)?$') { throw 'Invalid version' }
    if ($Private) {
      if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { throw 'Install GitHub CLI and run gh auth login' }
      $argsList = @('release','download','--repo',$Repository,'--pattern',$asset,'--pattern','SHA256SUMS','--dir',$temp)
      if ($Version -ne 'latest') { $argsList += $Version }
      & gh @argsList
      if ($LASTEXITCODE -ne 0) { throw 'Release download failed' }
    } else {
      $base = if ($Version -eq 'latest') { "https://github.com/$Repository/releases/latest/download" } else { "https://github.com/$Repository/releases/download/$Version" }
      [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
      Invoke-WebRequest -UseBasicParsing "$base/$asset" -OutFile (Join-Path $temp $asset)
      Invoke-WebRequest -UseBasicParsing "$base/SHA256SUMS" -OutFile (Join-Path $temp 'SHA256SUMS')
    }
  }
  $lines = @(Get-Content (Join-Path $temp 'SHA256SUMS') | Where-Object { $_ -match ('^[a-fA-F0-9]{64}\s+' + [regex]::Escape($asset) + '$') })
  if ($lines.Count -ne 1) { throw 'Missing or ambiguous checksum' }
  $expected = ($lines[0] -split '\s+')[0]
  if ((Get-FileHash (Join-Path $temp $asset) -Algorithm SHA256).Hash -ne $expected) { throw 'SHA-256 mismatch; installation unchanged' }
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip = [IO.Compression.ZipFile]::OpenRead((Join-Path $temp $asset))
  try {
    $entry = $zip.GetEntry('bgmcat-windows-amd64/bgmcat.exe')
    if (-not $entry) { throw 'Package executable missing' }
    [IO.Compression.ZipFileExtensions]::ExtractToFile($entry,(Join-Path $temp 'bgmcat.exe'))
  } finally { $zip.Dispose() }
  & (Join-Path $temp 'bgmcat.exe') --version
  if ($LASTEXITCODE -ne 0) { throw 'Executable verification failed' }
  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  $target = Join-Path $InstallDir 'bgmcat.exe'
  if (Test-Path $target) { Copy-Item $target "$target.previous" -Force }
  Copy-Item (Join-Path $temp 'bgmcat.exe') "$target.new" -Force
  Move-Item "$target.new" $target -Force
  Write-Host "Installed: $target"
  Write-Host 'Start manually. Stop the existing process before upgrading on Windows. Application data is preserved.'
} finally { Remove-Item -LiteralPath $temp -Recurse -Force }
