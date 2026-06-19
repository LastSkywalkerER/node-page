#!/usr/bin/env bash
#
# proxmox-lxc.sh — create a Debian LXC on a Proxmox VE host and install
# node-stats (native single binary + systemd) inside it. Inspired by the
# community-scripts.org (Proxmox VE Helper-Scripts) one-liners, but fully
# self-contained — it does not depend on their build.func framework, so you
# can run it straight from this repo.
#
# Run ON THE PROXMOX VE HOST (the hypervisor), as root:
#
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/proxmox-lxc.sh)"
#
# Subcommands (default: create):
#   create              create the LXC and install node-stats
#   update <ctid>       self-update node-stats inside an existing container
#   uninstall <ctid>    stop + destroy the container (asks first)
#
# Non-interactive overrides (env vars):
#   CTID, CT_HOSTNAME, DISK_SIZE (GB), CORE_COUNT, RAM_SIZE (MB),
#   BRIDGE (vmbr0), NET (dhcp | <CIDR>), GATEWAY, DNS, UNPRIVILEGED (1|0),
#   ENABLE_NESTING (1|0), START_ON_BOOT (1|0), HTTP_PORT (8080),
#   TEMPLATE_STORAGE, CONTAINER_STORAGE, NODE_STATS_VERSION (vX.Y.Z),
#   VERBOSE (1 to stream apt output). Set ADVANCED=1 for the whiptail wizard.
#
set -euo pipefail

REPO="LastSkywalkerER/node-page"
APP="node-stats"
HTTP_PORT_DEFAULT="${HTTP_PORT:-8080}"

# ---- pretty output (mirrors the community-scripts look) --------------------
if [ -t 1 ]; then
  RD=$'\033[01;31m'; GN=$'\033[1;92m'; YW=$'\033[33m'; BL=$'\033[36m'; CL=$'\033[m'
else
  RD=''; GN=''; YW=''; BL=''; CL=''
fi
CM="${GN}✓${CL}"; CROSS="${RD}✗${CL}"; INFO="${BL}ℹ${CL}"
msg_info() { printf ' %s %s...\n' "${YW}…${CL}" "$1"; }
msg_ok()   { printf ' %s %s\n' "$CM" "$1"; }
msg_warn() { printf ' %s %s\n' "${YW}!${CL}" "$1"; }
msg_error(){ printf ' %s %s\n' "$CROSS" "$1" >&2; }
die() { msg_error "$1"; exit 1; }

# Run a command quietly unless VERBOSE=1.
STD="silent"
[ "${VERBOSE:-0}" = "1" ] && STD="verbose"
run() { if [ "$STD" = verbose ]; then "$@"; else "$@" >/dev/null 2>&1; fi; }

header() {
  printf '%s' "$BL"
  cat <<'EOF'
   _   _           _        ____  _        _
  | \ | | ___   __| | ___  / ___|| |_ __ _| |_ ___
  |  \| |/ _ \ / _` |/ _ \ \___ \| __/ _` | __/ __|
  | |\  | (_) | (_| |  __/  ___) | || (_| | |_\__ \
  |_| \_|\___/ \__,_|\___| |____/ \__\__,_|\__|___/   LXC on Proxmox VE
EOF
  printf '%s\n' "$CL"
}

# ---- preflight -------------------------------------------------------------
preflight() {
  [ "$(id -u)" = "0" ] || die "run this on the Proxmox VE host as root."
  command -v pveversion >/dev/null 2>&1 || die "this is not a Proxmox VE host (pveversion not found). Run it on the hypervisor, not inside a VM/LXC."
  command -v pct >/dev/null 2>&1 || die "pct (LXC tooling) not found — is this a full PVE install?"
  for c in pveam pvesm; do command -v "$c" >/dev/null 2>&1 || die "$c not found."; done
}

# next free CTID (>= 100). Prefer pvesh; fall back to scanning pct list.
next_ctid() {
  local id
  id="$(pvesh get /cluster/nextid 2>/dev/null || true)"
  if [ -z "$id" ]; then
    id=100
    while pct status "$id" >/dev/null 2>&1 || qm status "$id" >/dev/null 2>&1; do id=$((id + 1)); done
  fi
  printf '%s' "$id"
}

# first storage that advertises a given content type (vztmpl / rootdir).
storages_for() { pvesm status -content "$1" 2>/dev/null | awk 'NR>1 {print $1}'; }
first_storage() { storages_for "$1" | head -1; }

HOST_ARCH=""
detect_arch() {
  HOST_ARCH="$(dpkg --print-architecture 2>/dev/null || echo amd64)"
  case "$HOST_ARCH" in amd64 | arm64) : ;; *) HOST_ARCH=amd64 ;; esac
}

# ---- settings --------------------------------------------------------------
set_defaults() {
  CTID="${CTID:-$(next_ctid)}"
  # NB: read CT_HOSTNAME, not HOSTNAME — bash auto-sets $HOSTNAME to the PVE
  # host's name, which would otherwise override our default and name the
  # container after the hypervisor.
  CT_HOSTNAME="${CT_HOSTNAME:-node-stats}"
  DISK_SIZE="${DISK_SIZE:-4}"
  CORE_COUNT="${CORE_COUNT:-1}"
  RAM_SIZE="${RAM_SIZE:-1024}"
  BRIDGE="${BRIDGE:-vmbr0}"
  NET="${NET:-dhcp}"
  GATEWAY="${GATEWAY:-}"
  DNS="${DNS:-}"
  UNPRIVILEGED="${UNPRIVILEGED:-1}"
  ENABLE_NESTING="${ENABLE_NESTING:-1}"
  START_ON_BOOT="${START_ON_BOOT:-1}"
  HTTP_PORT="${HTTP_PORT_DEFAULT}"
  TEMPLATE_STORAGE="${TEMPLATE_STORAGE:-$(first_storage vztmpl)}"
  CONTAINER_STORAGE="${CONTAINER_STORAGE:-$(first_storage rootdir)}"
  [ -n "$TEMPLATE_STORAGE" ] || die "no storage with 'vztmpl' content type found (need somewhere to keep the LXC template, usually 'local')."
  [ -n "$CONTAINER_STORAGE" ] || die "no storage with 'rootdir' content type found (need somewhere for the container rootfs, e.g. 'local-lvm')."

  # Proxmox auto-connect: the app self-attaches to this hypervisor on first
  # boot. Default on; disable with PROXMOX_AUTOCONNECT=0. The script (running on
  # the PVE host as root) mints a read-only API token unless you pass your own.
  PROXMOX_AUTOCONNECT="${PROXMOX_AUTOCONNECT:-1}"
  PROXMOX_URL="${PROXMOX_URL:-}"                       # default: https://<host-ip>:8006
  PROXMOX_USER="${PROXMOX_USER:-node-stats@pam}"
  PROXMOX_ROLE="${PROXMOX_ROLE:-PVEAuditor}"           # read-only; sees all nodes/guests/storage
  PROXMOX_TOKEN_NAME="${PROXMOX_TOKEN_NAME:-nodestats-c${CTID}}"
  PROXMOX_TOKEN_ID="${PROXMOX_TOKEN_ID:-}"             # full id (user@realm!name) if you bring your own
  PROXMOX_TOKEN_SECRET="${PROXMOX_TOKEN_SECRET:-}"
  PROXMOX_SKIP_TLS_VERIFY="${PROXMOX_SKIP_TLS_VERIFY:-1}"
}

# host_ipv4 — the PVE host's primary IPv4 (the address a container reaches it on).
host_ipv4() {
  local ip
  ip="$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.*src \([0-9.]*\).*/\1/p' | head -1)"
  [ -z "$ip" ] && ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  printf '%s' "$ip"
}

# gen_secret — 64 hex chars (openssl, /dev/urandom fallback).
gen_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

is_off() { case "$(printf '%s' "$1" | tr 'A-Z' 'a-z')" in 0 | false | off | no) return 0 ;; *) return 1 ;; esac; }

# whiptail wizard (ADVANCED=1 or no env CTID + a terminal). Falls back silently.
advanced_settings() {
  command -v whiptail >/dev/null 2>&1 || { msg_warn "whiptail missing — using defaults."; return; }
  exec 3>/dev/tty || { msg_warn "no terminal — using defaults."; return; }
  local v
  v=$(whiptail --inputbox "Container ID (CTID)" 8 60 "$CTID" --title "node-stats LXC" 3>&1 1>&2 2>&3) && CTID="$v"
  v=$(whiptail --inputbox "Hostname" 8 60 "$CT_HOSTNAME" --title "node-stats LXC" 3>&1 1>&2 2>&3) && CT_HOSTNAME="$v"
  v=$(whiptail --inputbox "Disk size (GB)" 8 60 "$DISK_SIZE" --title "node-stats LXC" 3>&1 1>&2 2>&3) && DISK_SIZE="$v"
  v=$(whiptail --inputbox "CPU cores" 8 60 "$CORE_COUNT" --title "node-stats LXC" 3>&1 1>&2 2>&3) && CORE_COUNT="$v"
  v=$(whiptail --inputbox "RAM (MB)" 8 60 "$RAM_SIZE" --title "node-stats LXC" 3>&1 1>&2 2>&3) && RAM_SIZE="$v"
  v=$(whiptail --inputbox "Network bridge" 8 60 "$BRIDGE" --title "node-stats LXC" 3>&1 1>&2 2>&3) && BRIDGE="$v"
  v=$(whiptail --inputbox "IP (dhcp or CIDR e.g. 192.168.1.50/24)" 8 70 "$NET" --title "node-stats LXC" 3>&1 1>&2 2>&3) && NET="$v"
  if [ "$NET" != dhcp ]; then
    v=$(whiptail --inputbox "Gateway" 8 60 "$GATEWAY" --title "node-stats LXC" 3>&1 1>&2 2>&3) && GATEWAY="$v"
  fi
  if whiptail --yesno "Unprivileged container? (recommended)" 8 60 --title "node-stats LXC" 3>&1 1>&2 2>&3; then UNPRIVILEGED=1; else UNPRIVILEGED=0; fi
  exec 3>&- || true
}

confirm_settings() {
  cat <<EOF

 ${INFO} Creating ${GN}${APP}${CL} LXC with:
    CTID            ${BL}${CTID}${CL}
    Hostname        ${BL}${CT_HOSTNAME}${CL}
    OS              ${BL}Debian 12${CL} (${HOST_ARCH})
    Cores / RAM     ${BL}${CORE_COUNT}${CL} / ${BL}${RAM_SIZE} MB${CL}
    Disk            ${BL}${DISK_SIZE} GB${CL} on ${BL}${CONTAINER_STORAGE}${CL}
    Network         ${BL}${BRIDGE}${CL} ip=${BL}${NET}${CL}${GATEWAY:+ gw=$GATEWAY}
    Unprivileged    ${BL}${UNPRIVILEGED}${CL}   Nesting ${BL}${ENABLE_NESTING}${CL}   On-boot ${BL}${START_ON_BOOT}${CL}
    HTTP port       ${BL}${HTTP_PORT}${CL}
    Template store  ${BL}${TEMPLATE_STORAGE}${CL}

EOF
}

# ---- template --------------------------------------------------------------
ensure_template() {
  local search="debian-12-standard"
  msg_info "Resolving the Debian 12 LXC template"
  # already downloaded on the chosen storage?
  TEMPLATE="$(pveam list "$TEMPLATE_STORAGE" 2>/dev/null | awk '{print $1}' \
    | sed 's|.*vztmpl/||' | grep -E "^${search}.*${HOST_ARCH}.*\.tar\.(zst|gz|xz)$" \
    | sort -V | tail -1)"
  if [ -z "${TEMPLATE:-}" ]; then
    run pveam update || true
    TEMPLATE="$(pveam available -section system 2>/dev/null \
      | awk -v s="$search" -v a="$HOST_ARCH" '$0 ~ s && $0 ~ a {print $NF}' \
      | sort -V | tail -1)"
    [ -n "$TEMPLATE" ] || die "could not find a ${search}_*_${HOST_ARCH} template via 'pveam available'."
    msg_info "Downloading template ${TEMPLATE} to ${TEMPLATE_STORAGE}"
    run pveam download "$TEMPLATE_STORAGE" "$TEMPLATE" || die "template download failed."
  fi
  TEMPLATE_VOLID="${TEMPLATE_STORAGE}:vztmpl/${TEMPLATE}"
  msg_ok "Template ready: ${TEMPLATE}"
}

# ---- create ----------------------------------------------------------------
build_netline() {
  local line="name=eth0,bridge=${BRIDGE}"
  if [ "$NET" = dhcp ]; then
    line="${line},ip=dhcp"
  else
    line="${line},ip=${NET}"
    [ -n "$GATEWAY" ] && line="${line},gw=${GATEWAY}"
  fi
  printf '%s' "$line"
}

create_lxc() {
  pct status "$CTID" >/dev/null 2>&1 && die "CTID ${CTID} already exists — pick another (set CTID=...)."
  msg_info "Creating LXC ${CTID}"
  local args=(
    "$CTID" "$TEMPLATE_VOLID"
    --hostname "$CT_HOSTNAME"
    --cores "$CORE_COUNT"
    --memory "$RAM_SIZE"
    --swap "$RAM_SIZE"
    --rootfs "${CONTAINER_STORAGE}:${DISK_SIZE}"
    --net0 "$(build_netline)"
    --unprivileged "$UNPRIVILEGED"
    --onboot "$START_ON_BOOT"
    --ostype debian
    --tags "node-stats;monitoring"
  )
  [ -n "$DNS" ] && args+=(--nameserver "$DNS")
  [ "$ENABLE_NESTING" = "1" ] && args+=(--features "nesting=1")
  run pct create "${args[@]}" || die "pct create failed (re-run with VERBOSE=1 to see the error)."
  msg_ok "Created LXC ${CTID}"

  msg_info "Starting LXC ${CTID}"
  run pct start "$CTID" || die "pct start failed."
  # wait for an IPv4 (DHCP) / networking to settle
  local ip=""
  for _ in $(seq 1 30); do
    ip="$(pct exec "$CTID" -- hostname -I 2>/dev/null | awk '{print $1}')" || true
    [ -n "$ip" ] && break
    sleep 1
  done
  CONTAINER_IP="${ip:-unknown}"
  msg_ok "Started — container IP ${CONTAINER_IP}"
}

# ---- install node-stats inside the container -------------------------------
install_app() {
  msg_info "Installing dependencies inside the container"
  run pct exec "$CTID" -- bash -c 'export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y -qq curl ca-certificates' \
    || die "apt dependency install failed."
  msg_ok "Dependencies installed"

  msg_info "Installing ${APP} (latest release .deb)"
  # Resolve tag + arch and apt-install the .deb (its postinstall enables the
  # node-stats systemd service on :8080). NODE_STATS_VERSION pins a release.
  pct exec "$CTID" -- bash -c "
    set -e
    export DEBIAN_FRONTEND=noninteractive
    REPO='${REPO}'
    ARCH=\$(dpkg --print-architecture)
    TAG='${NODE_STATS_VERSION:-}'
    if [ -z \"\$TAG\" ]; then
      TAG=\$(curl -fsSL https://api.github.com/repos/\$REPO/releases/latest | sed -n 's/.*\"tag_name\": *\"\\([^\"]*\\)\".*/\\1/p' | head -1)
    fi
    [ -n \"\$TAG\" ] || { echo 'could not resolve latest release tag'; exit 1; }
    DEB=\"node-stats_\${TAG#v}_\${ARCH}.deb\"
    echo \"  downloading \$DEB\"
    curl -fsSL -o /tmp/\$DEB \"https://github.com/\$REPO/releases/download/\$TAG/\$DEB\"
    apt-get install -y -qq /tmp/\$DEB
    rm -f /tmp/\$DEB
  " || die "node-stats install failed (re-run with VERBOSE=1)."
  msg_ok "${APP} installed"

  # Apply a custom HTTP port via a systemd drop-in (default service is :8080).
  if [ "$HTTP_PORT" != "8080" ]; then
    msg_info "Setting HTTP port to ${HTTP_PORT}"
    run pct exec "$CTID" -- bash -c "
      mkdir -p /etc/systemd/system/node-stats.service.d
      printf '[Service]\nEnvironment=ADDR=:${HTTP_PORT}\n' > /etc/systemd/system/node-stats.service.d/port.conf
      systemctl daemon-reload && systemctl restart node-stats
    " || msg_warn "could not apply custom port; service stays on :8080"
    msg_ok "HTTP port set to ${HTTP_PORT}"
  fi
}

# Mint (or accept) a read-only Proxmox API token and hand it to the container,
# so node-stats auto-attaches to this hypervisor on first boot. Best-effort:
# every failure degrades to "add the connector in the UI" without aborting the
# install. Idempotent: an already-existing token is left untouched (its secret
# can't be read back, so rotating it would break the existing connector).
AUTOCONNECT_NOTE=""
configure_proxmox_autoconnect() {
  if is_off "$PROXMOX_AUTOCONNECT"; then
    msg_warn "Proxmox auto-connect disabled (PROXMOX_AUTOCONNECT=0)."
    return
  fi

  local url token_id secret
  url="${PROXMOX_URL:-https://$(host_ipv4):8006}"

  if [ -n "$PROXMOX_TOKEN_ID" ] && [ -n "$PROXMOX_TOKEN_SECRET" ]; then
    token_id="$PROXMOX_TOKEN_ID"
    secret="$PROXMOX_TOKEN_SECRET"
    msg_ok "Using the Proxmox API token from env"
  else
    command -v pveum >/dev/null 2>&1 || { msg_warn "pveum not found — skipping auto-connect (add the connector in the UI)."; return; }
    token_id="${PROXMOX_USER}!${PROXMOX_TOKEN_NAME}"
    msg_info "Issuing read-only Proxmox API token ${token_id} (role ${PROXMOX_ROLE})"
    run pveum user add "$PROXMOX_USER" --comment "node-stats monitoring (auto-created)" 2>/dev/null || true
    run pveum acl modify / -user "$PROXMOX_USER" -role "$PROXMOX_ROLE" ||
      { msg_warn "could not grant ${PROXMOX_ROLE} to ${PROXMOX_USER} — skipping auto-connect."; return; }
    # PVE API tokens live at the datacenter level and OUTLIVE the container, so
    # a leftover from a previous run on this CTID is still here after a
    # destroy+recreate. Rotate it (remove + re-add): a freshly created container
    # has an empty DB (no connector yet), so re-issuing the secret can't orphan
    # anything — and we can't read an existing token's secret back to reuse it.
    run pveum user token remove "$PROXMOX_USER" "$PROXMOX_TOKEN_NAME" 2>/dev/null || true
    local out
    out="$(pveum user token add "$PROXMOX_USER" "$PROXMOX_TOKEN_NAME" --privsep 0 --output-format json 2>/dev/null)"
    secret="$(printf '%s' "$out" | sed -n 's/.*"value"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
    [ -n "$secret" ] || { msg_warn "could not create/parse the API token — skipping auto-connect."; return; }
    msg_ok "Issued Proxmox API token ${token_id}"
  fi

  local skip=true
  is_off "$PROXMOX_SKIP_TLS_VERIFY" && skip=false

  # Pre-seed stable JWT/REFRESH secrets (so the encrypted token survives the
  # setup wizard + restarts) and hand the Proxmox creds to the app via its .env.
  local jwt refresh
  jwt="$(gen_secret)"
  refresh="$(gen_secret)"
  msg_info "Wiring auto-connect into the container (${url})"
  pct exec "$CTID" -- bash -c "
    set -e
    mkdir -p /var/lib/node-stats
    f=/var/lib/node-stats/.env
    touch \"\$f\"; chmod 600 \"\$f\"
    set_kv() { if grep -q \"^\$1=\" \"\$f\" 2>/dev/null; then sed -i \"s#^\$1=.*#\$1=\$2#\" \"\$f\"; else echo \"\$1=\$2\" >>\"\$f\"; fi; }
    set_kv JWT_SECRET '${jwt}'
    set_kv REFRESH_SECRET '${refresh}'
    set_kv NODE_STATS_PROXMOX_URL '${url}'
    set_kv NODE_STATS_PROXMOX_TOKEN_ID '${token_id}'
    set_kv NODE_STATS_PROXMOX_TOKEN_SECRET '${secret}'
    set_kv NODE_STATS_PROXMOX_SKIP_TLS_VERIFY '${skip}'
    systemctl restart node-stats
  " || { msg_warn "could not wire auto-connect into the container."; return; }
  msg_ok "Proxmox auto-connect configured → app will attach to ${url} on boot"
  AUTOCONNECT_NOTE="${url}"
}

summary() {
  local connector_line
  if [ -n "$AUTOCONNECT_NOTE" ]; then
    connector_line=" ${CM} Auto-connected to Proxmox at ${BL}${AUTOCONNECT_NOTE}${CL} — the
    hypervisor + every VM/LXC appear automatically once you finish setup."
  else
    connector_line=" ${INFO} The container monitors itself out of the box. To see the Proxmox
    host + every VM/LXC, add a ${BL}Proxmox connector${CL} after setup
    (admin → Connectors). See docs/PROXMOX.md."
  fi
  cat <<EOF

 ${CM} ${GN}${APP}${CL} is up in LXC ${BL}${CTID}${CL}.

   Open:   ${GN}http://${CONTAINER_IP}:${HTTP_PORT}${CL}
   Finish setup in the browser (create the admin, pick the database).

${connector_line}

 ${INFO} Manage this container:
    Update:     ${YW}bash proxmox-lxc.sh update ${CTID}${CL}
    Console:    ${YW}pct enter ${CTID}${CL}   ·   logs: ${YW}pct exec ${CTID} -- journalctl -u node-stats -f${CL}
    Remove:     ${YW}bash proxmox-lxc.sh uninstall ${CTID}${CL}

EOF
}

# ---- subcommands -----------------------------------------------------------
cmd_create() {
  header
  preflight
  detect_arch
  set_defaults
  # Open the wizard when explicitly asked, or when interactive and nothing was preset.
  if [ "${ADVANCED:-0}" = "1" ]; then advanced_settings; fi
  confirm_settings
  ensure_template
  create_lxc
  install_app
  configure_proxmox_autoconnect
  summary
}

cmd_update() {
  preflight
  local id="${1:-}"
  [ -n "$id" ] || die "usage: proxmox-lxc.sh update <ctid>"
  pct status "$id" >/dev/null 2>&1 || die "no container ${id}."
  msg_info "Self-updating node-stats in LXC ${id}"
  run pct start "$id" || true
  pct exec "$id" -- bash -c '/usr/bin/node-stats update && systemctl restart node-stats' \
    || die "update failed."
  msg_ok "Updated node-stats in LXC ${id}"
}

cmd_uninstall() {
  preflight
  local id="${1:-}"
  [ -n "$id" ] || die "usage: proxmox-lxc.sh uninstall <ctid>"
  pct status "$id" >/dev/null 2>&1 || die "no container ${id}."
  msg_warn "This will STOP and DESTROY container ${id} and all its data."
  if [ -e /dev/tty ]; then
    printf ' Type the CTID (%s) to confirm: ' "$id" >/dev/tty
    local reply=""; IFS= read -r reply </dev/tty || true
    [ "$reply" = "$id" ] || die "aborted."
  fi
  run pct stop "$id" || true
  run pct destroy "$id" || die "destroy failed."
  msg_ok "Container ${id} removed."
}

case "${1:-create}" in
create) cmd_create ;;
update) cmd_update "${2:-}" ;;
uninstall) cmd_uninstall "${2:-}" ;;
-h | --help | help) printf 'usage: proxmox-lxc.sh {create|update <ctid>|uninstall <ctid>}\n' ;;
*) die "unknown command '${1}' (use create|update|uninstall)" ;;
esac
