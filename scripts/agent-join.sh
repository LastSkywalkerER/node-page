#!/usr/bin/env bash
# node-stats: one-command agent install + cluster join.
#
# Generated for you by the admin panel (Admin → Nodes → Raft cluster sync →
# "One-shot join token"); the command looks like:
#
#   NODE_STATS_JOIN_URL="http://10.0.0.2:9090" \
#   NODE_STATS_JOIN_KEY="<64-hex one-shot token>" \
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/agent-join.sh)"
#
# What it does on a fresh machine:
#   1. installs the Docker stack via scripts/install.sh (docker + compose v2
#      checks, stack dir, gen-compose, `docker compose up -d`),
#   2. waits for the app to come up,
#   3. joins the cluster through the same API the setup wizard uses
#      (POST /setup/join-raft-cluster) — the node id, bind and advertise
#      addresses are derived automatically,
#   4. waits until the leader's snapshot has replicated.
#
# Optional env knobs:
#   NODE_STATS_REPO            owner/name for the raw scripts (default LastSkywalkerER/node-page)
#   NODE_STATS_IMAGE           container image override
#   NODE_STATS_PORT            HTTP port (default 9090)
#   NODE_STATS_RAFT_PORT       Raft TCP port (default 7000)
#   NODE_STATS_DIR             stack directory override
#   NODE_STATS_HOSTNAME        display name for this machine
#   NODE_STATS_ADVERTISE_ADDR  host:port other nodes use to reach this node's
#                              Raft (default: primary IPv4 + RAFT_PORT)
#   NODE_STATS_SKIP_INSTALL=1  stack already running — only join
set -euo pipefail

REPO="${NODE_STATS_REPO:-LastSkywalkerER/node-page}"
PORT="${NODE_STATS_PORT:-9090}"
RAFT_PORT="${NODE_STATS_RAFT_PORT:-7000}"
API="http://127.0.0.1:${PORT}/api/v1"

log()  { printf '\033[1;36m[node-stats]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[node-stats]\033[0m %s\n' "$*" >&2; exit 1; }

[ -n "${NODE_STATS_JOIN_URL:-}" ] || fail "NODE_STATS_JOIN_URL is required (the main node's URL, e.g. http://10.0.0.2:9090)"
[ -n "${NODE_STATS_JOIN_KEY:-}" ] || fail "NODE_STATS_JOIN_KEY is required (one-shot token from the main node's admin panel)"
command -v curl >/dev/null 2>&1 || fail "curl is required"

# json_int <key> — best-effort flat-JSON integer extraction without jq.
json_int() { grep -o "\"$1\":[0-9]*" | head -1 | grep -o '[0-9]*$' || true; }
json_bool_true() { grep -q "\"$1\":true"; }

detect_ipv4() {
  if [ -n "${NODE_STATS_IPV4:-}" ]; then printf '%s' "$NODE_STATS_IPV4"; return; fi
  case "$(uname -s)" in
    Linux)
      ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<NF;i++) if ($i=="src") {print $(i+1); exit}}'
      ;;
    Darwin)
      ipconfig getifaddr "$(route -n get default 2>/dev/null | awk '/interface:/{print $2}')" 2>/dev/null
      ;;
  esac
}

# 1. Install the stack (reuses the canonical installer so there is exactly
#    one source of truth for docker checks / compose generation).
if [ "${NODE_STATS_SKIP_INSTALL:-0}" != "1" ]; then
  log "Installing the node-stats Docker stack…"
  tmp_install="$(mktemp)"
  curl -fsSL "https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh" -o "$tmp_install" \
    || fail "cannot download install.sh from github.com/${REPO}"
  bash "$tmp_install" install
  rm -f "$tmp_install"
fi

# 1b. The installer may have auto-picked non-default ports when 9090/7000
# were taken — the stack's .env is the source of truth from here on.
stack_dir="${NODE_STATS_DIR:-}"
if [ -z "$stack_dir" ]; then
  if [ "$(id -u)" = "0" ]; then stack_dir="/opt/node-stats"; else stack_dir="$HOME/.node-stats"; fi
fi
if [ -f "${stack_dir}/.env" ]; then
  env_port="$(sed -n 's/^NODE_STATS_PORT=//p' "${stack_dir}/.env" | head -1)"
  env_raft="$(sed -n 's/^NODE_STATS_RAFT_PORT=//p' "${stack_dir}/.env" | head -1)"
  [ -n "$env_port" ] && PORT="$env_port" && API="http://127.0.0.1:${PORT}/api/v1"
  [ -n "$env_raft" ] && RAFT_PORT="$env_raft"
fi

# 2. Wait for the app.
log "Waiting for the app on :${PORT}…"
status_json=""
for _ in $(seq 1 60); do
  status_json="$(curl -fsS -m 3 "${API}/setup/status" 2>/dev/null)" && break
  sleep 2
done
[ -n "$status_json" ] || fail "the app did not come up on :${PORT} — check 'docker compose logs' in the stack dir"

# 3. Join — only meaningful on a fresh (setup-mode) node.
if ! printf '%s' "$status_json" | json_bool_true "setup_needed"; then
  progress="$(curl -fsS -m 5 "${API}/setup/raft-progress" 2>/dev/null || true)"
  if printf '%s' "$progress" | json_bool_true "enabled"; then
    log "This node is already part of a cluster — nothing to do."
    exit 0
  fi
  fail "this node is already set up; joining replaces local data and must be confirmed from Admin → Nodes → Cluster sync → Join"
fi

advertise="${NODE_STATS_ADVERTISE_ADDR:-}"
if [ -z "$advertise" ]; then
  ip4="$(detect_ipv4)"
  [ -n "$ip4" ] || fail "could not auto-detect this machine's IPv4 — set NODE_STATS_ADVERTISE_ADDR=host:${RAFT_PORT}"
  advertise="${ip4}:${RAFT_PORT}"
fi

log "Joining cluster at ${NODE_STATS_JOIN_URL} (this node advertises ${advertise})…"
join_body="$(printf '{"peer_url":"%s","token":"%s","advertise_addr":"%s","bind_addr":":%s"}' \
  "$NODE_STATS_JOIN_URL" "$NODE_STATS_JOIN_KEY" "$advertise" "$RAFT_PORT")"
join_resp="$(curl -sS -m 60 -X POST "${API}/setup/join-raft-cluster" \
  -H 'Content-Type: application/json' -d "$join_body" -w '\n%{http_code}')"
join_code="$(printf '%s' "$join_resp" | tail -1)"
if [ "$join_code" != "200" ]; then
  printf '%s\n' "$join_resp" | head -n -1 >&2
  fail "join was rejected (HTTP ${join_code}). Token expired or already used? Generate a fresh one on the main node."
fi

# 4. Wait for the snapshot to replicate (applied catches up with commit).
log "Join accepted — waiting for the cluster snapshot to replicate…"
for _ in $(seq 1 90); do
  progress="$(curl -fsS -m 5 "${API}/setup/raft-progress" 2>/dev/null || true)"
  applied="$(printf '%s' "$progress" | json_int "applied_index")"
  commit="$(printf '%s' "$progress" | json_int "commit_index")"
  if printf '%s' "$progress" | json_bool_true "enabled" \
    && [ -n "$applied" ] && [ -n "$commit" ] && [ "$commit" -gt 0 ] && [ "$applied" -ge "$commit" ]; then
    log "Done. This machine is now a cluster node."
    log "Open http://${advertise%%:*}:${PORT} and sign in with your existing cluster credentials."
    exit 0
  fi
  sleep 2
done
fail "snapshot replication did not finish in time — check Admin → Nodes → Raft on the main node (the voter may still catch up on its own)"
