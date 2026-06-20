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
#                 NODE_STATS_CHANNEL=beta (follow the beta release line: :beta
#                   docker image / latest prerelease native asset; persisted)
#
set -euo pipefail

REPO="LastSkywalkerER/node-page"
RAW_BASE="https://raw.githubusercontent.com/${REPO}/main"
# Release channel: stable (default) or beta. Beta pulls the moving :beta docker
# image / the latest prerelease native asset and is persisted so the running app
# and the self-updater keep following it after install.
CHANNEL="$(printf '%s' "${NODE_STATS_CHANNEL:-stable}" | tr '[:upper:]' '[:lower:]')"
[ "$CHANNEL" = beta ] || CHANNEL=stable
DEFAULT_IMAGE="ghcr.io/lastskywalkerer/node-page:latest"
[ "$CHANNEL" = beta ] && DEFAULT_IMAGE="ghcr.io/lastskywalkerer/node-page:beta"
IMAGE="${NODE_STATS_IMAGE:-$DEFAULT_IMAGE}"
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

# port_busy PORT — true when something already listens on PORT on this host
# (incl. docker-proxy published ports). ss first, /proc fallback, then a
# loopback connect probe as the last resort.
port_busy() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    local listeners
    if listeners=$(ss -ltnH 2>/dev/null); then
      printf '%s\n' "$listeners" | awk '{print $4}' | grep -Eq "[:.]${port}\$"
      return $?
    fi
  fi
  # /proc/net/tcp6 is absent on IPv6-disabled hosts; an unopenable file makes
  # awk exit 2 even when the END rule matched, so list only readable files.
  local hex files=""
  hex=$(printf '%04X' "$port")
  [ -r /proc/net/tcp ] && files="/proc/net/tcp"
  [ -r /proc/net/tcp6 ] && files="$files /proc/net/tcp6"
  if [ -n "$files" ]; then
    # shellcheck disable=SC2086
    awk -v hx=":${hex}" '$4 == "0A" && index($2, hx) == length($2) - length(hx) + 1 {found=1} END {exit !found}' $files 2>/dev/null && return 0
    return 1
  fi
  (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null && { exec 3>&- 3<&- || true; return 0; }
  return 1
}

# pick_free_port DEFAULT LABEL EXPLICIT [AVOID] — prints a usable port. An
# explicitly requested port that is busy fails loudly (never silently override
# the operator); a busy DEFAULT scans upwards for the next free one. AVOID is
# a port already claimed by this very run (no listener yet, so port_busy
# can't see it).
pick_free_port() {
  local want="$1" label="$2" explicit="$3" avoid="${4:-}" p
  if [ "$want" != "$avoid" ] && ! port_busy "$want"; then
    printf '%s' "$want"
    return
  fi
  if [ "$explicit" = "1" ]; then
    die "${label} port ${want} is already in use on this host — free it or choose another via the env var."
  fi
  for p in $(seq $((want + 1)) $((want + 100))); do
    [ "$p" = "$avoid" ] && continue
    if ! port_busy "$p"; then
      yellow "${label} port ${want} is busy — using ${p} instead" >&2
      printf '%s' "$p"
      return
    fi
  done
  die "no free ${label} port found in ${want}-$((want + 100))"
}

# pick_ports mutates HTTP_PORT / RAFT_PORT on a FRESH install only: an
# existing .env keeps its ports (the stack already owns those listeners, and
# re-picking would orphan the cluster's advertise addresses).
pick_ports() {
  [ -f "$STACK_DIR/.env" ] && return 0
  local http_explicit=0 raft_explicit=0
  [ -n "${NODE_STATS_PORT:-}" ] && http_explicit=1
  [ -n "${NODE_STATS_RAFT_PORT:-}" ] && raft_explicit=1
  HTTP_PORT="$(pick_free_port "$HTTP_PORT" "HTTP (NODE_STATS_PORT)" "$http_explicit")"
  RAFT_PORT="$(pick_free_port "$RAFT_PORT" "Raft (NODE_STATS_RAFT_PORT)" "$raft_explicit" "$HTTP_PORT")"
}

# env_get KEY — value from the stack .env ('' when absent).
env_get() { sed -n "s/^$1=//p" "$STACK_DIR/.env" 2>/dev/null | head -1; }

# env_set KEY VALUE — update-or-append KEY in the stack .env.
env_set() {
  local env="$STACK_DIR/.env"
  if grep -q "^$1=" "$env" 2>/dev/null; then
    sed -i.bak "s#^$1=.*#$1=$2#" "$env" && rm -f "$env.bak"
  else
    echo "$1=$2" >>"$env"
  fi
}

# confirm PROMPT — y/N from the operator's terminal. With `curl | bash` stdin
# holds the script itself, so ask via /dev/tty. Non-interactive runs answer
# through NODE_STATS_REPAIR=1 (yes) / 0 (no). Returns 2 when there is no way
# to ask at all.
confirm() {
  case "${NODE_STATS_REPAIR:-}" in
  1 | y | yes | true) return 0 ;;
  0 | n | no | false) return 1 ;;
  esac
  # -r/-w only check permission bits; without a controlling terminal the
  # open itself fails (ENXIO), so probe by actually opening it.
  if (: </dev/tty) 2>/dev/null; then
    local reply=""
    printf '%s [y/N] ' "$1" >/dev/tty 2>/dev/null || true
    IFS= read -r reply </dev/tty 2>/dev/null || true
    case "$reply" in
    [Yy] | [Yy][Ee][Ss]) return 0 ;;
    esac
    return 1
  fi
  return 2
}

# app_container_state PROJECT — docker state of the app service left by a
# previous run: running/exited/created/restarting, or '' when none exists.
app_container_state() {
  docker ps -a \
    --filter "label=com.docker.compose.project=$1" \
    --filter "label=com.docker.compose.service=node-stats" \
    --format '{{.State}}' 2>/dev/null | head -1
}

# check_existing_install — a stack dir from an earlier run was found. Verify
# that deployment actually works before silently reusing its configuration:
# a previous attempt may have died half-way (classic: .env pinned to a port
# that another service owns → 'Bind … port is already allocated', containers
# left created-but-never-started). Healthy → continue as a normal in-place
# refresh. Broken → explain what is wrong and offer to recreate ONLY the
# node-stats containers: data, secrets and settings in the stack dir are
# kept, and clashing ports are re-picked once our own listeners are gone.
check_existing_install() {
  { [ -f "$STACK_DIR/.env" ] || [ -f "$STACK_DIR/docker-compose.yml" ]; } || return 0

  local port raft project state http_ok=0 i
  port="$(env_get NODE_STATS_PORT)"
  port="${port:-$HTTP_PORT}"
  raft="$(env_get NODE_STATS_RAFT_PORT)"
  raft="${raft:-$RAFT_PORT}"
  project="$(env_get COMPOSE_PROJECT_NAME)"
  project="${project:-$PROJECT}"
  state="$(app_container_state "$project")"

  if [ "$state" = running ]; then
    for i in 1 2 3; do
      if curl -fsS -m 3 "http://127.0.0.1:${port}/api/v1/health" >/dev/null 2>&1; then
        http_ok=1
        break
      fi
      [ "$i" -lt 3 ] && sleep 2
    done
    if [ "$http_ok" = 1 ]; then
      green "Existing node-stats install found in ${STACK_DIR} — it is healthy, refreshing it in place."
      HTTP_PORT="$port"
      RAFT_PORT="$raft"
      return 0
    fi
  fi

  yellow "node-stats is already (partially) installed in ${STACK_DIR}, but it is not healthy:"
  if [ "$state" = running ]; then
    yellow "  - the app container is running, but its API does not answer on :${port}"
  elif [ -n "$state" ]; then
    yellow "  - the app container is '${state}' instead of running"
  else
    yellow "  - no app container exists (a previous install likely failed half-way)"
  fi
  if [ "$state" != running ]; then
    if port_busy "$raft"; then
      yellow "  - Raft port ${raft} (pinned in ${STACK_DIR}/.env) is held by another service — the previous start most likely failed with 'port is already allocated'"
    fi
    if port_busy "$port"; then
      yellow "  - HTTP port ${port} (pinned in ${STACK_DIR}/.env) is held by another service"
    fi
  fi

  yellow "Reinstalling recreates ONLY the node-stats containers — your data, secrets and settings in ${STACK_DIR} are kept, and clashing ports are re-picked automatically."
  local rc=0
  confirm "Reinstall the node-stats containers now?" || rc=$?
  if [ "$rc" = 2 ]; then
    die "cannot ask for confirmation (no terminal). Re-run with NODE_STATS_REPAIR=1 to reinstall automatically, or remove ${STACK_DIR} to start fresh."
  elif [ "$rc" != 0 ]; then
    die "left as-is. Re-run when ready, or remove ${STACK_DIR} to start completely fresh."
  fi

  green "Removing the old node-stats containers ..."
  if [ -f "$STACK_DIR/docker-compose.yml" ]; then
    compose down --remove-orphans >/dev/null 2>&1 || true
  fi
  docker ps -aq --filter "label=com.docker.compose.project=${project}" 2>/dev/null |
    xargs -r docker rm -f >/dev/null 2>&1 || true

  # Our own listeners are gone now — anything still on these ports is foreign.
  local http_explicit=0 raft_explicit=0
  [ -n "${NODE_STATS_PORT:-}" ] && http_explicit=1
  [ -n "${NODE_STATS_RAFT_PORT:-}" ] && raft_explicit=1
  [ "$http_explicit" = 1 ] && port="$NODE_STATS_PORT"
  [ "$raft_explicit" = 1 ] && raft="$NODE_STATS_RAFT_PORT"
  HTTP_PORT="$(pick_free_port "$port" "HTTP (NODE_STATS_PORT)" "$http_explicit")"
  if [ "$raft_explicit" = 0 ] && port_busy "$raft" &&
    grep -q '^RAFT_ENABLED=true' "$STACK_DIR/.env.agent" 2>/dev/null; then
    die "Raft port ${raft} is busy, but this node already joined a cluster whose peers reach it on that port — free the port (or change it cluster-wide) instead of silently moving it."
  fi
  RAFT_PORT="$(pick_free_port "$raft" "Raft (NODE_STATS_RAFT_PORT)" "$raft_explicit" "$HTTP_PORT")"
  env_set NODE_STATS_PORT "$HTTP_PORT"
  env_set NODE_STATS_RAFT_PORT "$RAFT_PORT"
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

# seed_channel_agent records the beta release channel in .env.agent (the file the
# dockerized app loads as /app/.env) so the running app + its self-updater follow
# beta. Never clobbers an existing value — once installed, the channel is owned by
# the in-app settings toggle, not the installer.
seed_channel_agent() {
  [ "$CHANNEL" = beta ] || return 0
  local f="$STACK_DIR/.env.agent"
  grep -q '^NODE_STATS_RELEASE_CHANNEL=' "$f" 2>/dev/null && return 0
  echo "NODE_STATS_RELEASE_CHANNEL=beta" >>"$f"
  green "Following the beta release channel (image ${IMAGE})."
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
  # Once the wizard has applied a topology (managed postgres etc.) the
  # CONTROLLER owns docker-compose.yml - desired-state.json marks that.
  # Regenerating the sqlite base here would silently drop the db service
  # and revert the app to sqlite on every update/repair.
  if [ -s "$STACK_DIR/docker-compose.yml" ] && [ -f "$STACK_DIR/data/docker/desired-state.json" ]; then
    yellow "Keeping controller-managed docker-compose.yml (wizard topology applied)"
    write_compose_override
    return
  fi
  # Base compose: generate from the image so it always matches the controller's
  # output; fall back to the committed template if the run fails.
  if ! docker run --rm -e NODE_STATS_IMAGE="$IMAGE" "$IMAGE" gen-compose >"$STACK_DIR/docker-compose.yml" 2>/dev/null ||
    [ ! -s "$STACK_DIR/docker-compose.yml" ]; then
    yellow "gen-compose unavailable; fetching the base template"
    fetch "${RAW_BASE}/install/docker-compose.base.yml" >"$STACK_DIR/docker-compose.yml"
    [ -s "$STACK_DIR/docker-compose.yml" ] || die "could not obtain a base docker-compose.yml"
  fi

  write_compose_override
}

# OS host-capabilities override (Linux gets pid/ipc host; macOS none) -
# installer-owned, safe to refresh on every run; the controller never touches it.
write_compose_override() {
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
  check_existing_install
  pick_ports
  prepare_stack_dir
  seed_channel_agent
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
  # Reclaim the dangling old image layers the pull replaced (dangling-only — never
  # touches images still in use). Best-effort: don't fail the update on prune errors.
  green "Pruning dangling images ..."
  docker image prune -f >/dev/null 2>&1 || yellow "image prune skipped (non-fatal)"
  local p
  p="$(env_get NODE_STATS_PORT)"
  green "Update complete → http://localhost:${p:-$HTTP_PORT}"
}

cmd_uninstall() {
  [ -f "$STACK_DIR/docker-compose.yml" ] || die "no stack at ${STACK_DIR}"
  green "Stopping node-stats ..."
  if [ "${1:-}" = "--purge" ]; then
    # --remove-orphans drops services dropped from compose (e.g. an old db);
    # -v deletes the named volumes (managed pgdata) along with the data dir.
    compose down --remove-orphans -v || true
    yellow "Purging data at ${STACK_DIR}/data ..."
    rm -rf "${STACK_DIR}/data"
  else
    compose down --remove-orphans || true
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
  if [ "$CHANNEL" = beta ]; then
    # Beta: newest non-draft release INCLUDING prereleases. /releases/latest skips
    # prereleases, so list /releases (newest-first) and take the first tag.
    fetch "https://api.github.com/repos/${REPO}/releases?per_page=20" |
      sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1
    return
  fi
  fetch "https://api.github.com/repos/${REPO}/releases/latest" |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1
}

# seed_channel_native records the beta channel in the native install's .env
# (WorkingDirectory of the systemd unit), so the booted server and `node-stats
# update` follow beta. Never clobbers an existing value (UI-owned post-install).
seed_channel_native() {
  [ "$CHANNEL" = beta ] || return 0
  mkdir -p "$NATIVE_DIR"
  local f="$NATIVE_DIR/.env"
  grep -q '^NODE_STATS_RELEASE_CHANNEL=' "$f" 2>/dev/null && return 0
  echo "NODE_STATS_RELEASE_CHANNEL=beta" >>"$f"
  green "Following the beta release channel."
}

# download_native_binary <dest> — fetch + checksum-verify + extract the
# linux/<arch> release binary into <dest>. NODE_STATS_LOCAL_TARBALL overrides the
# download (air-gapped / testing).
download_native_binary() {
  local dest="$1" tag debver asset url tmp
  tag="$(latest_release_tag)"
  [ -n "$tag" ] || die "could not determine the latest release (no published release yet, or no network). Pin one with NODE_STATS_VERSION=vX.Y.Z."
  # Releases publish per-OS installers only. On Linux we pull the .deb and
  # extract /usr/bin/node-stats from it (works without root via dpkg-deb / ar).
  # nfpm builds the prerelease .deb as 0.8.3~beta.14, but GitHub sanitizes '~'
  # to '.' in the served asset name → node-stats_0.8.3.beta.14_amd64.deb. So map
  # the tag's '-' to '.' for the download (stable tags have no '-', unchanged).
  debver="${tag#v}"; debver="${debver//-/.}"
  asset="node-stats_${debver}_${ARCH}.deb"
  tmp="$(mktemp -d)"
  if [ -n "${NODE_STATS_LOCAL_DEB:-}" ]; then
    cp "$NODE_STATS_LOCAL_DEB" "$tmp/$asset" || die "local .deb not found: $NODE_STATS_LOCAL_DEB"
  else
    url="https://github.com/${REPO}/releases/download/${tag}/${asset}"
    green "Downloading ${asset} ..."
    fetch "$url" >"$tmp/$asset"
    [ -s "$tmp/$asset" ] || die "download failed: $url"
  fi
  if command -v dpkg-deb >/dev/null 2>&1; then
    dpkg-deb -x "$tmp/$asset" "$tmp/x" || { rm -rf "$tmp"; die "extract failed"; }
  elif command -v ar >/dev/null 2>&1; then
    ( cd "$tmp" && ar x "$asset" ) || { rm -rf "$tmp"; die "extract failed"; }
    mkdir -p "$tmp/x" && tar -xf "$tmp"/data.tar.* -C "$tmp/x" || { rm -rf "$tmp"; die "extract failed"; }
  else
    rm -rf "$tmp"; die "need 'dpkg-deb' or 'ar' to unpack the .deb"
  fi
  [ -f "$tmp/x/usr/bin/node-stats" ] || { rm -rf "$tmp"; die "binary not found in .deb"; }
  mkdir -p "$(dirname "$dest")"
  install -m 0755 "$tmp/x/usr/bin/node-stats" "$dest" 2>/dev/null || { cp "$tmp/x/usr/bin/node-stats" "$dest" && chmod 0755 "$dest"; }
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
  seed_channel_native
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
  # Run from the data dir so `node-stats update` loads its .env and follows the
  # release channel the admin picked in the UI (persisted there), not just stable.
  ( cd "$NATIVE_DIR" && "$bin" update ) || die "update failed"
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
