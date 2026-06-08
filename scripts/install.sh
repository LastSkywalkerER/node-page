#!/usr/bin/env bash
#
# install.sh — install / update / uninstall node-stats.
#
#   curl -fsSL https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/install.sh | bash
#
# Subcommands (pass with `| bash -s -- <cmd>`):
#   install            (default) Docker Compose deployment: pull image, bring up stack
#   update             pull the latest image and recreate
#   uninstall          stop the stack (add --purge to also delete data)
#   native             NATIVE single-binary install (no Docker) — ideal for SBCs
#                      (Orange Pi / Raspberry Pi, linux amd64 + arm64); installs a
#                      systemd service. Real host metrics (reads /proc directly).
#   update-native      self-update the native binary and restart the service
#   uninstall-native   remove the native service + binary (add --purge for data)
#
# Env overrides:  NODE_STATS_DIR   NODE_STATS_PORT   NODE_STATS_IMAGE
#                 NODE_STATS_VERSION (pin a vX.Y.Z release for native)
#
set -euo pipefail

REPO="LastSkywalkerER/node-page"
RAW_BASE="https://raw.githubusercontent.com/${REPO}/main"
IMAGE="${NODE_STATS_IMAGE:-ghcr.io/lastskywalkerer/node-page:latest}"
HTTP_PORT="${NODE_STATS_PORT:-9090}"
RAFT_PORT="${NODE_STATS_RAFT_PORT:-7000}"
# Compose project name. Override (along with distinct ports + dir) to run more
# than one instance on the same host.
PROJECT="${NODE_STATS_PROJECT:-node-stats}"

if [ -n "${NODE_STATS_DIR:-}" ]; then
  STACK_DIR="$NODE_STATS_DIR"
elif [ "$(id -u)" = "0" ]; then
  STACK_DIR="/opt/node-stats"
else
  STACK_DIR="$HOME/.node-stats"
fi

# --- Native install layout (no Docker) ---
SERVICE="node-stats"
NATIVE_PORT="${NODE_STATS_PORT:-8080}" # native default :8080 (docker uses 9090)
if [ -n "${NODE_STATS_DIR:-}" ]; then
  NATIVE_DIR="$NODE_STATS_DIR"
elif [ "$(id -u)" = "0" ]; then
  NATIVE_DIR="/var/lib/node-stats"
else
  NATIVE_DIR="$HOME/.node-stats"
fi
if [ "$(id -u)" = "0" ]; then NATIVE_BIN_DIR="/usr/local/bin"; else NATIVE_BIN_DIR="$HOME/.local/bin"; fi

green() { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
red() { printf '\033[31m%s\033[0m\n' "$*" >&2; }
die() {
  red "error: $*"
  exit 1
}

OS=""
ARCH=""
detect_platform() {
  case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "unsupported OS '$(uname -s)'. On Windows use the PowerShell installer (see README)." ;;
  esac
  case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "unsupported architecture '$(uname -m)' (need x86_64/amd64 or arm64)." ;;
  esac
  green "Detected ${OS}/${ARCH}"
}

require_docker() {
  command -v docker >/dev/null 2>&1 ||
    die "Docker not found. Install Docker (https://docs.docker.com/get-docker/), or on a lean host / SBC use the native install:
  curl -fsSL ${RAW_BASE}/scripts/install.sh | bash -s -- native"
  docker compose version >/dev/null 2>&1 ||
    die "Docker Compose v2 not found. Update Docker, or install the compose plugin: https://docs.docker.com/compose/install/"
  docker info >/dev/null 2>&1 ||
    die "Docker daemon not reachable. Start Docker and re-run."
}

# fetch URL to stdout (curl or wget), empty on failure (caller falls back).
fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" 2>/dev/null || true
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$1" 2>/dev/null || true
  fi
}

# detect_host_ipv4 resolves the IPv4 of the interface that reaches the internet
# (the default route's source address). Run on the HOST so it sees the real LAN
# IP — a container behind a bridge network only sees its docker IP.
detect_host_ipv4() {
  local ip=""
  if [ "${OS:-}" = darwin ]; then
    local iface
    iface="$(route -n get default 2>/dev/null | awk '/interface:/{print $2}')"
    [ -n "$iface" ] && ip="$(ipconfig getifaddr "$iface" 2>/dev/null || true)"
  else
    ip="$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.*src \([0-9.]*\).*/\1/p' | head -1)"
    [ -z "$ip" ] && ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  fi
  printf '%s' "$ip"
}

prepare_stack_dir() {
  mkdir -p "$STACK_DIR/data/docker"
  # .env.agent MUST be a regular file: it is bind-mounted to /app/.env and the
  # wizard writes secrets/config there. A directory mount silently swallows that.
  if [ -d "$STACK_DIR/.env.agent" ]; then
    die "$STACK_DIR/.env.agent is a directory — remove it (rmdir '$STACK_DIR/.env.agent') and re-run."
  fi
  [ -f "$STACK_DIR/.env.agent" ] || : >"$STACK_DIR/.env.agent"
  chmod 600 "$STACK_DIR/.env.agent" 2>/dev/null || true
}

write_env() {
  local env="$STACK_DIR/.env"
  if [ -f "$env" ]; then
    # Preserve existing settings; just ensure the stack dir is current.
    if grep -q '^NODE_STATS_STACK_HOST_DIR=' "$env"; then
      sed -i.bak "s#^NODE_STATS_STACK_HOST_DIR=.*#NODE_STATS_STACK_HOST_DIR=${STACK_DIR}#" "$env" && rm -f "$env.bak"
    else
      echo "NODE_STATS_STACK_HOST_DIR=${STACK_DIR}" >>"$env"
    fi
    return
  fi
  local host_ip
  host_ip="$(detect_host_ipv4)"
  [ -n "$host_ip" ] && green "Detected host IPv4: ${host_ip}"
  cat >"$env" <<EOF
# node-stats stack configuration (Compose variable substitution).
# JWT_SECRET / REFRESH_SECRET are generated by the setup wizard into .env.agent.
NODE_STATS_IMAGE=${IMAGE}
NODE_STATS_PORT=${HTTP_PORT}
NODE_STATS_RAFT_PORT=${RAFT_PORT}
NODE_STATS_HOSTNAME=${NODE_STATS_HOSTNAME:-}
NODE_STATS_STACK_HOST_DIR=${STACK_DIR}
# Host's outbound-interface IPv4 (detected on the host) so the wizard / cluster
# join URLs use a routable address instead of the container's docker IP.
NODE_STATS_IPV4=${host_ip}
COMPOSE_PROJECT_NAME=${PROJECT}
GIN_MODE=release
EOF
  chmod 600 "$env" 2>/dev/null || true
}

write_compose() {
  # Base compose: generate from the image so it always matches the controller's
  # output; fall back to the committed template if the run fails.
  if ! docker run --rm -e NODE_STATS_IMAGE="$IMAGE" "$IMAGE" gen-compose >"$STACK_DIR/docker-compose.yml" 2>/dev/null ||
    [ ! -s "$STACK_DIR/docker-compose.yml" ]; then
    yellow "gen-compose unavailable; fetching the base template"
    fetch "${RAW_BASE}/install/docker-compose.base.yml" >"$STACK_DIR/docker-compose.yml"
    [ -s "$STACK_DIR/docker-compose.yml" ] || die "could not obtain a base docker-compose.yml"
  fi

  # OS host-capabilities override (Linux gets pid/ipc host; macOS none).
  local src="docker-compose.macos.yml"
  [ "$OS" = linux ] && src="docker-compose.linux.yml"
  local override
  override="$(fetch "${RAW_BASE}/install/${src}")"
  if [ -n "$override" ]; then
    printf '%s\n' "$override" >"$STACK_DIR/docker-compose.override.yml"
  elif [ "$OS" = linux ]; then
    cat >"$STACK_DIR/docker-compose.override.yml" <<'EOF'
services:
  node-stats:
    pid: host
    ipc: host
EOF
  else
    printf 'services:\n  node-stats: {}\n' >"$STACK_DIR/docker-compose.override.yml"
  fi

  if [ "$OS" != linux ]; then
    yellow "Note: Docker Desktop runs in a VM — host CPU/mem/disk metrics reflect the VM, not your machine."
    yellow "For full host metrics on macOS/Windows, use the native binary (see README)."
  fi
}

compose() { (cd "$STACK_DIR" && docker compose "$@"); }

cmd_install() {
  detect_platform
  require_docker
  green "Installing node-stats into ${STACK_DIR}"
  prepare_stack_dir
  green "Pulling ${IMAGE} ..."
  if ! docker pull "$IMAGE" >/dev/null 2>&1; then
    docker image inspect "$IMAGE" >/dev/null 2>&1 || die "failed to pull ${IMAGE} (and no local copy present)"
    yellow "pull failed; using the local ${IMAGE}"
  fi
  write_env
  write_compose
  green "Starting the stack ..."
  compose pull >/dev/null 2>&1 || true
  compose up -d || die "docker compose up failed"
  green ""
  green "node-stats is up → http://localhost:${HTTP_PORT}"
  green "Open it in a browser to finish setup (create the admin, pick the database)."
  yellow "Manage later: curl -fsSL ${RAW_BASE}/scripts/install.sh | bash -s -- update | uninstall"
  yellow "Stack dir: ${STACK_DIR}"
}

cmd_update() {
  require_docker
  [ -f "$STACK_DIR/docker-compose.yml" ] || die "no stack at ${STACK_DIR}; run install first."
  green "Updating node-stats in ${STACK_DIR} ..."
  compose pull
  compose up -d
  green "Update complete → http://localhost:${HTTP_PORT}"
}

cmd_uninstall() {
  [ -f "$STACK_DIR/docker-compose.yml" ] || die "no stack at ${STACK_DIR}"
  green "Stopping node-stats ..."
  compose down || true
  if [ "${1:-}" = "--purge" ]; then
    yellow "Purging data at ${STACK_DIR}/data ..."
    rm -rf "${STACK_DIR}/data"
  else
    yellow "Data kept at ${STACK_DIR}/data (use 'uninstall --purge' to delete)."
  fi
}

# ----------------------------------------------------------------------------
# Native install (no Docker) — for SBCs (Orange Pi / Raspberry Pi) and lean hosts
# ----------------------------------------------------------------------------

# latest_release_tag prints the newest release tag (e.g. v1.2.3). Honour an
# explicit NODE_STATS_VERSION override.
latest_release_tag() {
  if [ -n "${NODE_STATS_VERSION:-}" ]; then
    printf '%s' "$NODE_STATS_VERSION"
    return
  fi
  fetch "https://api.github.com/repos/${REPO}/releases/latest" |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1
}

# download_native_binary <dest> — fetch + checksum-verify + extract the
# linux/<arch> release binary into <dest>. NODE_STATS_LOCAL_TARBALL overrides the
# download (air-gapped / testing).
download_native_binary() {
  local dest="$1" tag asset url tmp
  tag="$(latest_release_tag)"
  [ -n "$tag" ] || die "could not determine the latest release (no published release yet, or no network). Pin one with NODE_STATS_VERSION=vX.Y.Z."
  asset="node-stats_${tag}_linux_${ARCH}.tar.gz"
  tmp="$(mktemp -d)"
  if [ -n "${NODE_STATS_LOCAL_TARBALL:-}" ]; then
    cp "$NODE_STATS_LOCAL_TARBALL" "$tmp/$asset" || die "local tarball not found: $NODE_STATS_LOCAL_TARBALL"
  else
    url="https://github.com/${REPO}/releases/download/${tag}/${asset}"
    green "Downloading ${asset} ..."
    fetch "$url" >"$tmp/$asset"
    [ -s "$tmp/$asset" ] || die "download failed: $url"
    local sums want got
    sums="$(fetch "https://github.com/${REPO}/releases/download/${tag}/SHA256SUMS")"
    want="$(printf '%s\n' "$sums" | awk -v a="$asset" '{gsub(/^\*/,"",$2)} $2==a {print $1}' | head -1)"
    if [ -n "$want" ] && command -v sha256sum >/dev/null 2>&1; then
      got="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
      [ "$want" = "$got" ] || { rm -rf "$tmp"; die "checksum mismatch for $asset"; }
      green "checksum OK"
    fi
  fi
  tar -xzf "$tmp/$asset" -C "$tmp" || { rm -rf "$tmp"; die "extract failed"; }
  [ -f "$tmp/node-stats" ] || { rm -rf "$tmp"; die "binary 'node-stats' not found in archive"; }
  mkdir -p "$(dirname "$dest")"
  install -m 0755 "$tmp/node-stats" "$dest" 2>/dev/null || { cp "$tmp/node-stats" "$dest" && chmod 0755 "$dest"; }
  rm -rf "$tmp"
}

# install_systemd_unit <bin> <workdir> <port> — returns non-zero when systemd
# isn't usable (not root / no systemctl), so the caller can fall back.
install_systemd_unit() {
  local bin="$1" wd="$2" port="$3"
  command -v systemctl >/dev/null 2>&1 || return 1
  [ "$(id -u)" = "0" ] || return 1
  cat >"/etc/systemd/system/${SERVICE}.service" <<EOF
[Unit]
Description=node-stats monitoring
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=${bin}
WorkingDirectory=${wd}
Environment=ADDR=:${port}
Environment=GIN_MODE=release
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now "${SERVICE}" >/dev/null 2>&1
}

cmd_install_native() {
  detect_platform
  [ "$OS" = linux ] || die "native install is Linux-only. On macOS/Windows download the binary from the Releases page."
  green "Installing node-stats (native, no Docker) for linux/${ARCH}"
  mkdir -p "$NATIVE_DIR"
  local bin="$NATIVE_BIN_DIR/node-stats"
  download_native_binary "$bin"
  green "Installed → $bin   (data/config dir: $NATIVE_DIR)"
  if install_systemd_unit "$bin" "$NATIVE_DIR" "$NATIVE_PORT"; then
    green "systemd service '${SERVICE}' enabled and started."
  else
    yellow "No systemd (or not root). Run it manually:"
    yellow "  cd '$NATIVE_DIR' && ADDR=:${NATIVE_PORT} '$bin'"
  fi
  green ""
  green "node-stats is up → http://localhost:${NATIVE_PORT}"
  green "Open it in a browser to finish setup. Native reads the host's /proc directly — real host metrics."
  yellow "Manage: install.sh update-native | uninstall-native"
}

cmd_update_native() {
  detect_platform
  [ "$OS" = linux ] || die "native update is Linux-only."
  local bin="$NATIVE_BIN_DIR/node-stats"
  [ -x "$bin" ] || die "no native install found at $bin"
  green "Self-updating $bin ..."
  "$bin" update || die "update failed"
  if command -v systemctl >/dev/null 2>&1 && [ -f "/etc/systemd/system/${SERVICE}.service" ]; then
    systemctl restart "${SERVICE}" && green "restarted ${SERVICE}"
  else
    yellow "Restart node-stats to apply the new version."
  fi
}

cmd_uninstall_native() {
  if command -v systemctl >/dev/null 2>&1 && [ -f "/etc/systemd/system/${SERVICE}.service" ]; then
    systemctl disable --now "${SERVICE}" 2>/dev/null || true
    rm -f "/etc/systemd/system/${SERVICE}.service"
    systemctl daemon-reload 2>/dev/null || true
  fi
  rm -f "$NATIVE_BIN_DIR/node-stats"
  if [ "${1:-}" = "--purge" ]; then
    rm -rf "$NATIVE_DIR"
    yellow "Purged data at $NATIVE_DIR"
  else
    yellow "Data kept at $NATIVE_DIR (use 'uninstall-native --purge' to delete)."
  fi
  green "Native node-stats uninstalled."
}

case "${1:-install}" in
install) cmd_install ;;
update) cmd_update ;;
uninstall) cmd_uninstall "${2:-}" ;;
native) cmd_install_native ;;
update-native) cmd_update_native ;;
uninstall-native) cmd_uninstall_native "${2:-}" ;;
-h | --help | help) echo "usage: install.sh {install|update|uninstall [--purge] | native|update-native|uninstall-native [--purge]}" ;;
*) die "unknown command '${1}' (use install|update|uninstall|native|update-native|uninstall-native)" ;;
esac
