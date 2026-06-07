#!/usr/bin/env bash
#
# localcluster.sh — spin up a 2-node Raft cluster on this machine for testing.
#
# Each node runs from its own working dir under .localtest/ so that the
# CWD-relative .env, data/raft, and stats.db stay isolated. Logs are teed
# to .localtest/nodeN/node.log so you can tail everything in one place.
#
#   node1: HTTP :9091  Raft 127.0.0.1:7001  (bootstrap leader)
#   node2: HTTP :9092  Raft 127.0.0.1:7002  (joins node1)
#
# Usage:
#   scripts/localcluster.sh build      # compile the binary
#   scripts/localcluster.sh up         # start both nodes (background)
#   scripts/localcluster.sh bootstrap  # complete setup on node1 + issue join token
#   scripts/localcluster.sh join       # node2 joins the cluster
#   scripts/localcluster.sh status     # raft status from the leader
#   scripts/localcluster.sh logs       # tail -f both logs
#   scripts/localcluster.sh down       # stop both nodes
#   scripts/localcluster.sh reset      # down + wipe .localtest data (keeps binary)
#   scripts/localcluster.sh all        # build + up + bootstrap + join + status
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$ROOT/.localtest"
BIN="$TEST_DIR/node-stats"

N1_HTTP="http://127.0.0.1:9091"
N2_HTTP="http://127.0.0.1:9092"

ADMIN_EMAIL="admin@local.test"
ADMIN_PASS="localtest123"

JWT="localtest-jwt-secret-change-me"
REFRESH="localtest-refresh-secret-change-me"

green() { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
red() { printf '\033[31m%s\033[0m\n' "$*"; }

write_env() {
  # $1 = node dir, $2 = HTTP addr (:9091), $3 = raft port, $4 = bootstrap(true/false), $5 = node id, $6 = raft_enabled(true/empty)
  local dir="$1" addr="$2" rport="$3" boot="$4" nid="$5" renabled="$6"
  mkdir -p "$dir/data"
  {
    echo "ADDR=$addr"
    echo "GIN_MODE=debug"
    echo "DEBUG=true"
    echo "DB_TYPE=sqlite"
    echo "DB_DSN=stats.db"
    echo "JWT_SECRET=$JWT"
    echo "REFRESH_SECRET=$REFRESH"
    if [ "$renabled" = "true" ]; then
      echo "RAFT_ENABLED=true"
      echo "RAFT_CLUSTER_ID=local"
      echo "RAFT_NODE_ID=$nid"
      echo "RAFT_BIND_ADDR=127.0.0.1:$rport"
      echo "RAFT_ADVERTISE_ADDR=127.0.0.1:$rport"
      echo "RAFT_DATA_DIR=./data/raft"
      echo "RAFT_BOOTSTRAP=$boot"
    fi
  } > "$dir/.env"
}

cmd_build() {
  mkdir -p "$TEST_DIR"
  green "Building binary → $BIN"
  ( cd "$ROOT" && go build -o "$BIN" ./cmd/server )
  green "Build OK"
}

cmd_init() {
  # node1: raft enabled + bootstrap leader.
  write_env "$TEST_DIR/node1" ":9091" 7001 true  node1 true
  # node2: NO raft in .env — it gets activated by the join flow (raftActivator).
  write_env "$TEST_DIR/node2" ":9092" 7002 false node2 ""
  green "Wrote node1/.env (bootstrap leader) and node2/.env (fresh joiner)"
}

start_node() {
  local dir="$1" name="$2"
  if [ -f "$dir/node.pid" ] && kill -0 "$(cat "$dir/node.pid")" 2>/dev/null; then
    yellow "$name already running (pid $(cat "$dir/node.pid"))"
    return
  fi
  # `exec` replaces the subshell with the binary, so $! is the binary's own
  # pid (not a wrapper that leaks the server when killed).
  ( cd "$dir" && exec "$BIN" >"$dir/node.log" 2>&1 ) &
  echo $! > "$dir/node.pid"
  green "$name started (pid $(cat "$dir/node.pid")) → $dir/node.log"
}

cmd_up() {
  [ -x "$BIN" ] || cmd_build
  cmd_init
  start_node "$TEST_DIR/node1" node1
  sleep 1
  start_node "$TEST_DIR/node2" node2
  sleep 1
  yellow "Logs: tail -f $TEST_DIR/node1/node.log $TEST_DIR/node2/node.log"
}

cmd_bootstrap() {
  green "Completing setup on node1 ($N1_HTTP) — creates admin + activates leader"
  curl -fsS -X POST "$N1_HTTP/api/v1/setup/complete" \
    -H 'Content-Type: application/json' \
    -d "{\"admin_email\":\"$ADMIN_EMAIL\",\"admin_password\":\"$ADMIN_PASS\",\"config\":{\"jwt_secret\":\"$JWT\",\"refresh_secret\":\"$REFRESH\",\"addr\":\":9091\",\"db_type\":\"sqlite\",\"db_dsn\":\"stats.db\",\"raft_enabled\":\"true\",\"raft_cluster_id\":\"local\",\"raft_node_id\":\"node1\",\"raft_bind_addr\":\"127.0.0.1:7001\",\"raft_advertise_addr\":\"127.0.0.1:7001\",\"raft_data_dir\":\"./data/raft\",\"raft_bootstrap\":\"true\"}}" \
    && echo
  green "Logging in as admin to grab a token"
  TOKEN_JSON=$(curl -fsS -c "$TEST_DIR/cookies1.txt" -X POST "$N1_HTTP/api/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}")
  echo "$TOKEN_JSON" | head -c 300; echo
  green "Issuing a Raft join token (admin)"
  JOIN_JSON=$(curl -fsS -b "$TEST_DIR/cookies1.txt" -X POST "$N1_HTTP/api/v1/raft/join-token" \
    -H 'Content-Type: application/json' -d '{"ttl_minutes":60}')
  echo "$JOIN_JSON"
  echo "$JOIN_JSON" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p' > "$TEST_DIR/join.token"
  green "Join token saved → $TEST_DIR/join.token"
}

cmd_join() {
  local token; token="$(cat "$TEST_DIR/join.token")"
  green "node2 joining cluster via node1 ($N1_HTTP) with token ${token:0:8}…"
  curl -fsS -X POST "$N2_HTTP/api/v1/setup/join-raft-cluster" \
    -H 'Content-Type: application/json' \
    -d "{\"peer_url\":\"$N1_HTTP\",\"token\":\"$token\",\"node_id\":\"node2\",\"bind_addr\":\"127.0.0.1:7002\",\"advertise_addr\":\"127.0.0.1:7002\",\"advertise_url\":\"$N2_HTTP\"}" \
    && echo
}

cmd_status() {
  green "Raft status from node1 (leader):"
  curl -fsS -b "$TEST_DIR/cookies1.txt" "$N1_HTTP/api/v1/raft/status" | sed 's/,/,\n  /g'
  echo
}

cmd_logs() { tail -f "$TEST_DIR/node1/node.log" "$TEST_DIR/node2/node.log"; }

stop_node() {
  local dir="$1" name="$2"
  if [ -f "$dir/node.pid" ]; then
    local pid; pid="$(cat "$dir/node.pid")"
    if kill -0 "$pid" 2>/dev/null; then kill "$pid" && green "$name stopped (pid $pid)"; fi
    rm -f "$dir/node.pid"
  else
    yellow "$name not running"
  fi
}

cmd_down() {
  stop_node "$TEST_DIR/node1" node1
  stop_node "$TEST_DIR/node2" node2
}

cmd_reset() {
  cmd_down
  rm -rf "$TEST_DIR/node1" "$TEST_DIR/node2" "$TEST_DIR"/cookies*.txt "$TEST_DIR/join.token"
  green "Wiped node data (binary kept)"
}

cmd_all() {
  cmd_build; cmd_up
  yellow "Waiting for node1 to elect itself leader…"; sleep 3
  cmd_bootstrap
  yellow "Waiting for leader to stabilize…"; sleep 2
  cmd_join
  sleep 3
  cmd_status
}

case "${1:-}" in
  build) cmd_build ;;
  init) cmd_init ;;
  up) cmd_up ;;
  bootstrap) cmd_bootstrap ;;
  join) cmd_join ;;
  status) cmd_status ;;
  logs) cmd_logs ;;
  down) cmd_down ;;
  reset) cmd_reset ;;
  all) cmd_all ;;
  *) echo "usage: $0 {build|up|bootstrap|join|status|logs|down|reset|all}"; exit 1 ;;
esac
