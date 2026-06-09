#!/bin/sh
set -e
# Data/config dir (SQLite db + wizard-written .env with JWT/REFRESH secrets live here).
mkdir -p /var/lib/node-stats
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  systemctl enable --now node-stats.service || true
fi
echo "node-stats installed → open http://localhost:8080 in a browser to finish setup."
