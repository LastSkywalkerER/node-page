#!/usr/bin/env bash
# diagnose-mac-collision.sh
#
# Catch the recurring host-registration failure coming from node-stats and, the
# MOMENT it shows up in the logs, snapshot the `hosts` table so the colliding
# row is captured while it exists:
#
#   ERRO Failed to upsert local host record
#        error="constraint failed: UNIQUE constraint failed: hosts.mac_address (2067)"
#   (Postgres twin: SQLSTATE 23505 on the hosts_mac unique index)
#
# Root cause it hunts for: the local collector row (id=1) can't refresh because
# some OTHER row already holds this node's MAC (docker bridge containers on
# different machines derive identical 02:42:… MACs). When id=1's upsert fails,
# last_seen never advances → the card shows "offline" even though metrics stream.
#
# Run it FROM CRON, on the node whose own instance logs the error. Each run only
# scans log lines since the previous run and dumps the DB only if the error is
# present in that window — so it's cheap and safe to fire every minute.
#
# Zero-config on the stock Docker+SQLite deploy. Override anything via env.

set -euo pipefail

# ------------------------------- config --------------------------------------
STATE_DIR="${NS_DIAG_STATE_DIR:-${HOME:-/tmp}/.node-stats-diag}"
OUT="${NS_DIAG_OUT:-$STATE_DIR/mac-collision.log}"
WINDOW="${NS_DIAG_WINDOW:-70s}"      # how far back to scan logs each run (>= cron period)
MAX_OUT_BYTES="${NS_DIAG_MAX_OUT_BYTES:-5242880}"  # rotate report past 5 MB

# error signature (SQLite 2067 + Postgres 23505 + the app's own message)
ERR_RE='Failed to (upsert local host|register/update current host)|UNIQUE constraint failed: hosts\.mac|hosts_mac|SQLSTATE 23505|\(2067\)|\(23505\)'

# Docker discovery (empty container => auto-detect by compose labels)
APP_CONTAINER="${NS_DIAG_CONTAINER:-}"
COMPOSE_PROJECT="${NODE_STATS_PROJECT:-node-stats}"
APP_SERVICE="${NODE_STATS_APP_SERVICE:-node-stats}"

# systemd fallback (native install)
SYSTEMD_UNIT="${NS_DIAG_UNIT:-node-stats}"

mkdir -p "$STATE_DIR"
STAMP="$(date '+%Y-%m-%d %H:%M:%S %z')"

emit() { printf '%s\n' "$*" >>"$OUT"; }
rule() { emit "════════════════════════════════════════════════════════════════════════"; }

rotate_if_big() {
  [ -f "$OUT" ] || return 0
  local sz
  sz=$(wc -c <"$OUT" 2>/dev/null || echo 0)
  if [ "$sz" -gt "$MAX_OUT_BYTES" ]; then
    mv -f "$OUT" "$OUT.1" 2>/dev/null || true
  fi
}

# --------------------------- locate the instance -----------------------------
detect_container() {
  [ -n "$APP_CONTAINER" ] && { echo "$APP_CONTAINER"; return; }
  command -v docker >/dev/null 2>&1 || return 0
  docker ps \
    --filter "label=com.docker.compose.project=$COMPOSE_PROJECT" \
    --filter "label=com.docker.compose.service=$APP_SERVICE" \
    --format '{{.Names}}' 2>/dev/null | head -1
}

grab_logs() { # $1=container (may be empty)
  local c="$1"
  if [ -n "$c" ]; then
    docker logs --since "$WINDOW" "$c" 2>&1
  elif command -v journalctl >/dev/null 2>&1; then
    journalctl -u "$SYSTEMD_UNIT" --since "-$WINDOW" --no-pager 2>/dev/null
  fi
}

inspect_env() { # $1=container $2=VAR -> value
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$1" 2>/dev/null \
    | sed -n "s/^$2=//p" | head -1
}

mount_source() { # $1=container $2=destination -> host path
  docker inspect -f \
    "{{range .Mounts}}{{if eq .Destination \"$2\"}}{{.Source}}{{end}}{{end}}" \
    "$1" 2>/dev/null
}

# ------------------------------- DB queries ----------------------------------
SQL_HOSTS='SELECT id,name,mac_address,ipv4,origin_cluster,source,last_seen FROM hosts ORDER BY id;'
SQL_DUPES='SELECT mac_address, COUNT(*) AS n, GROUP_CONCAT(id) AS ids FROM hosts WHERE mac_address <> "" GROUP BY mac_address HAVING n > 1;'
SQL_LOCALMAC='SELECT id,name,mac_address,origin_cluster,source,last_seen FROM hosts WHERE mac_address = (SELECT mac_address FROM hosts WHERE id=1) ORDER BY id;'

query_sqlite() { # $1=host db path
  local db="$1"
  [ -f "$db" ] || { emit "!! sqlite db not found at: $db"; return; }
  if command -v sqlite3 >/dev/null 2>&1; then
    for q in "all hosts:|$SQL_HOSTS" "duplicate MACs (the smoking gun):|$SQL_DUPES" "rows sharing THIS node's (id=1) MAC:|$SQL_LOCALMAC"; do
      emit "-- ${q%%|*}"
      sqlite3 -readonly -header -column "file:$db?mode=ro" "${q#*|}" 2>>"$OUT" >>"$OUT" || \
        sqlite3 -header -column "$db" "${q#*|}" 2>>"$OUT" >>"$OUT" || emit "   (query failed)"
      emit ""
    done
  elif command -v python3 >/dev/null 2>&1; then
    NS_DB="$db" NS_Q_HOSTS="$SQL_HOSTS" NS_Q_DUPES="$SQL_DUPES" NS_Q_LOCAL="$SQL_LOCALMAC" \
    python3 - >>"$OUT" 2>&1 <<'PY'
import os, sqlite3
db = os.environ["NS_DB"]
con = sqlite3.connect(f"file:{db}?mode=ro", uri=True, timeout=5)
for title, q in (("all hosts", "NS_Q_HOSTS"),
                 ("duplicate MACs (the smoking gun)", "NS_Q_DUPES"),
                 ("rows sharing THIS node's (id=1) MAC", "NS_Q_LOCAL")):
    print(f"-- {title}")
    cur = con.execute(os.environ[q])
    cols = [d[0] for d in cur.description]
    print(" | ".join(cols))
    for row in cur.fetchall():
        print(" | ".join("" if v is None else str(v) for v in row))
    print()
con.close()
PY
  else
    emit "!! neither sqlite3 nor python3 available — install one: apt-get install -y sqlite3"
  fi
}

query_postgres_docker() { # $1=app container, $2=dsn
  local dsn="$2" dbc
  # find the managed postgres container in the same compose project
  dbc="$(docker ps --filter "label=com.docker.compose.project=$COMPOSE_PROJECT" \
                    --filter "ancestor=postgres:16-alpine" --format '{{.Names}}' 2>/dev/null | head -1)"
  [ -z "$dbc" ] && dbc="$(docker ps --format '{{.Names}}' | grep -E "^${COMPOSE_PROJECT}[-_]db" | head -1)"
  if [ -z "$dbc" ]; then
    emit "!! postgres container not found; run manually: psql '$dsn' -c '$SQL_DUPES'"
    return
  fi
  # DSN is gorm key=value form: extract user/dbname for psql
  local user dbname
  user="$(printf '%s' "$dsn"   | tr ' ' '\n' | sed -n 's/^user=//p'   | head -1)"
  dbname="$(printf '%s' "$dsn" | tr ' ' '\n' | sed -n 's/^dbname=//p' | head -1)"
  user="${user:-postgres}"; dbname="${dbname:-node_stats}"
  for q in "all hosts:$SQL_HOSTS" "duplicate MACs (the smoking gun):$SQL_DUPES" "rows sharing THIS node's (id=1) MAC:$SQL_LOCALMAC"; do
    emit "-- ${q%%:*}"
    docker exec -e PGPASSWORD="$(printf '%s' "$dsn" | tr ' ' '\n' | sed -n 's/^password=//p' | head -1)" \
      "$dbc" psql -U "$user" -d "$dbname" -c "${q#*:}" >>"$OUT" 2>>"$OUT" || emit "   (query failed)"
    emit ""
  done
}

snapshot_db() { # $1=container (may be empty)
  local c="$1"
  if [ -n "$c" ]; then
    local dbtype dbdsn
    dbtype="$(inspect_env "$c" DB_TYPE)"; dbtype="${dbtype:-sqlite}"
    dbdsn="$(inspect_env "$c" DB_DSN)";   dbdsn="${dbdsn:-/app/data/stats.db}"
    emit "db: type=$dbtype dsn=$dbdsn (container=$c)"
    if [ "$dbtype" = "postgres" ]; then
      query_postgres_docker "$c" "$dbdsn"
    else
      # map the in-container sqlite path to the host bind-mount
      local host_data cfile hostdb
      host_data="$(mount_source "$c" /app/data)"
      cfile="${dbdsn##*/}"
      if [ -n "$host_data" ]; then hostdb="$host_data/$cfile"; else hostdb="$dbdsn"; fi
      emit "db(host path)=$hostdb"
      query_sqlite "$hostdb"
    fi
  else
    # native/systemd install: read DB from the runtime env file if present
    local envf dbtype dbdsn
    for envf in "${NS_DIAG_ENV_FILE:-}" ./.env.agent ./.env /app/data/.env /etc/node-stats/.env; do
      [ -n "$envf" ] && [ -f "$envf" ] && break || envf=""
    done
    dbtype="sqlite"; dbdsn="stats.db"
    if [ -n "$envf" ]; then
      # shellcheck disable=SC1090
      dbtype="$(sed -n 's/^DB_TYPE=//p' "$envf" | tail -1)"; dbtype="${dbtype:-sqlite}"
      dbdsn="$(sed -n 's/^DB_DSN=//p' "$envf"  | tail -1)"; dbdsn="${dbdsn:-stats.db}"
    fi
    emit "db: type=$dbtype dsn=$dbdsn (native; env=$envf)"
    if [ "$dbtype" = "postgres" ]; then
      if command -v psql >/dev/null 2>&1; then
        for q in "$SQL_HOSTS" "$SQL_DUPES" "$SQL_LOCALMAC"; do psql "$dbdsn" -c "$q" >>"$OUT" 2>>"$OUT" || true; done
      else emit "!! install psql or set NS_DIAG_ENV_FILE"; fi
    else
      query_sqlite "$dbdsn"
    fi
  fi
}

# --------------------------------- main --------------------------------------
rotate_if_big
container="$(detect_container || true)"
logs="$(grab_logs "$container" || true)"

if [ -z "$logs" ]; then
  # Nothing to read (container not found / no journal). Note once, quietly exit 0.
  exit 0
fi

hits="$(printf '%s\n' "$logs" | grep -Ec "$ERR_RE" || true)"
if [ "${hits:-0}" -eq 0 ]; then
  exit 0
fi

rule
emit "[$STAMP] MAC-collision error detected — $hits matching line(s) in last $WINDOW"
emit "host=$(hostname 2>/dev/null) container=${container:-<none/native>}"
emit ""
emit "-- last matching log lines --"
printf '%s\n' "$logs" | grep -E "$ERR_RE" | tail -5 >>"$OUT"
emit ""
snapshot_db "$container"
rule
emit ""
