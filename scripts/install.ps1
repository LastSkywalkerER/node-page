# install.ps1 — install / update / uninstall node-stats on Windows via Docker Desktop.
#
#   irm https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/install.ps1 | iex
#
# With a subcommand:
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/install.ps1))) update
#
# Note: Docker Desktop runs containers in a Linux VM, so host metrics reflect the
# VM, not your PC. For true host metrics use the native node-stats.exe (see README).
#
# Set $env:NODE_STATS_CHANNEL='beta' to follow the beta release line (the :beta
# image; persisted so the app + self-updater keep following it).
#Requires -Version 5.1
[CmdletBinding()]
param([string]$Command = 'install', [switch]$Purge)

$ErrorActionPreference = 'Stop'
$Repo    = 'LastSkywalkerER/node-page'
$RawBase = "https://raw.githubusercontent.com/$Repo/main"
# Release channel: stable (default) or beta. Beta pulls the moving :beta image
# and is persisted so the running app + self-updater keep following it.
$Channel = if ($env:NODE_STATS_CHANNEL) { $env:NODE_STATS_CHANNEL.ToLower() } else { 'stable' }
if ($Channel -ne 'beta') { $Channel = 'stable' }
$DefaultImage = if ($Channel -eq 'beta') { 'ghcr.io/lastskywalkerer/node-page:beta' } else { 'ghcr.io/lastskywalkerer/node-page:latest' }
$Image   = if ($env:NODE_STATS_IMAGE) { $env:NODE_STATS_IMAGE } else { $DefaultImage }
$Port    = if ($env:NODE_STATS_PORT)  { $env:NODE_STATS_PORT }  else { '9090' }
$StackDir = if ($env:NODE_STATS_DIR)  { $env:NODE_STATS_DIR }   else { Join-Path $env:USERPROFILE '.node-stats' }
$Project = 'node-stats'

function Require-Docker {
  if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw "Docker Desktop not found: https://docs.docker.com/desktop/install/windows-install/"
  }
  docker compose version *> $null
  if ($LASTEXITCODE -ne 0) { throw "Docker Compose v2 unavailable; update Docker Desktop." }
  docker info *> $null
  if ($LASTEXITCODE -ne 0) { throw "Docker daemon not running. Start Docker Desktop and retry." }
}

function Initialize-Stack {
  New-Item -ItemType Directory -Force -Path (Join-Path $StackDir 'data\docker') | Out-Null
  $envAgent = Join-Path $StackDir '.env.agent'
  if (Test-Path $envAgent -PathType Container) { throw "$envAgent is a directory; remove it and re-run." }
  if (-not (Test-Path $envAgent)) { New-Item -ItemType File -Path $envAgent | Out-Null }
}

function Seed-Channel {
  # Record the beta channel in .env.agent (loaded as /app/.env by the container)
  # so the running app + self-updater follow beta. Never clobber an existing
  # value — once installed, the channel is owned by the in-app settings toggle.
  if ($Channel -ne 'beta') { return }
  $envAgent = Join-Path $StackDir '.env.agent'
  $content = if (Test-Path $envAgent) { Get-Content $envAgent -Raw -ErrorAction SilentlyContinue } else { '' }
  if ($content -notmatch 'NODE_STATS_RELEASE_CHANNEL=') {
    Add-Content -Path $envAgent -Value 'NODE_STATS_RELEASE_CHANNEL=beta' -Encoding ascii
    Write-Host "Following the beta release channel (image $Image)." -ForegroundColor Green
  }
}

function Write-Env {
  $envFile = Join-Path $StackDir '.env'
  if (Test-Path $envFile) { return }
  @"
NODE_STATS_IMAGE=$Image
NODE_STATS_PORT=$Port
NODE_STATS_STACK_HOST_DIR=$StackDir
COMPOSE_PROJECT_NAME=$Project
GIN_MODE=release
"@ | Set-Content -Path $envFile -Encoding ascii
}

function Write-Compose {
  $base = Join-Path $StackDir 'docker-compose.yml'
  docker run --rm -e NODE_STATS_IMAGE="$Image" "$Image" gen-compose | Set-Content -Path $base -Encoding ascii
  if (-not (Test-Path $base) -or (Get-Item $base).Length -eq 0) {
    (Invoke-RestMethod "$RawBase/install/docker-compose.base.yml") | Set-Content -Path $base -Encoding ascii
  }
  # Docker Desktop = VM: use the no-host-mode override.
  "services:`n  node-stats: {}" | Set-Content -Path (Join-Path $StackDir 'docker-compose.override.yml') -Encoding ascii
  Write-Host "Note: Docker Desktop runs in a VM — host metrics reflect the VM, not your PC." -ForegroundColor Yellow
}

function Invoke-Compose { Push-Location $StackDir; try { docker compose @args } finally { Pop-Location } }

switch ($Command) {
  'install' {
    Require-Docker
    Write-Host "Installing node-stats into $StackDir" -ForegroundColor Green
    Initialize-Stack
    Seed-Channel
    docker pull $Image | Out-Null
    Write-Env
    Write-Compose
    Invoke-Compose up -d
    Write-Host "`nnode-stats is up -> http://localhost:$Port" -ForegroundColor Green
    Write-Host "Open it in a browser to finish setup." -ForegroundColor Green
  }
  'update' {
    Require-Docker
    Invoke-Compose pull
    Invoke-Compose up -d
    # Reclaim the dangling old image layers the pull replaced (best-effort).
    docker image prune -f | Out-Null
    Write-Host "Update complete -> http://localhost:$Port" -ForegroundColor Green
  }
  'uninstall' {
    if ($Purge) {
      Invoke-Compose down --remove-orphans -v
      Remove-Item -Recurse -Force (Join-Path $StackDir 'data') -ErrorAction SilentlyContinue
    } else {
      Invoke-Compose down --remove-orphans
    }
  }
  default { Write-Host "usage: install.ps1 [install|update|uninstall [-Purge]]" }
}
