#!/usr/bin/env bash
# Copyright (c) 2021-2025 community-scripts ORG
# Author: LastSkywalkerER
# License: MIT | https://github.com/community-scripts/ProxmoxVE/raw/main/LICENSE
# Source: https://github.com/LastSkywalkerER/node-page

source /dev/stdin <<<"$FUNCTIONS_FILE_PATH"
color
verb_ip6
catch_errors
setting_up_container
network_check
update_os

msg_info "Installing Dependencies"
$STD apt-get install -y curl ca-certificates
msg_ok "Installed Dependencies"

msg_info "Installing node-stats"
# Pull the matching linux/{amd64,arm64} release .deb. Its postinstall drops the
# binary into /usr/bin, creates the data dir (/var/lib/node-stats) and enables
# the node-stats systemd service on :8080 — no extra unit wiring needed here.
ARCH=$(dpkg --print-architecture)
RELEASE=$(curl -fsSL https://api.github.com/repos/LastSkywalkerER/node-page/releases/latest |
  grep '"tag_name":' | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
curl -fsSL "https://github.com/LastSkywalkerER/node-page/releases/download/${RELEASE}/node-stats_${RELEASE#v}_${ARCH}.deb" -o /tmp/node-stats.deb
$STD apt-get install -y /tmp/node-stats.deb
msg_ok "Installed node-stats ${RELEASE}"

motd_ssh
customize

msg_info "Cleaning up"
rm -f /tmp/node-stats.deb
$STD apt-get -y autoremove
$STD apt-get -y autoclean
msg_ok "Cleaned"
