# node-stats → community-scripts.org submission kit

These three files are formatted for the **[community-scripts/ProxmoxVE](https://github.com/community-scripts/ProxmoxVE)**
repository (the project behind <https://community-scripts.org>). They are *not*
run from this repo — they only work once merged into (or test-pushed to a fork
of) the ProxmoxVE repo, because `ct/node-stats.sh` sources that project's
`misc/build.func` and `build.func` then fetches `install/node-stats-install.sh`
from the same repo/branch.

```
ct/node-stats.sh              # host-side: defines the LXC, sources build.func
install/node-stats-install.sh # container-side: installs the node-stats .deb
json/node-stats.json          # website metadata card  → frontend/public/json/
```

> Want to deploy **right now**, without waiting on a PR? Use the self-contained
> [`scripts/proxmox-lxc.sh`](../../scripts/proxmox-lxc.sh) instead — it does the
> same thing with no dependency on the community-scripts framework. See
> [`docs/PROXMOX-LXC.md`](../../docs/PROXMOX-LXC.md).

## How it installs

The container-side script doesn't compile anything — it downloads the matching
`linux/{amd64,arm64}` **release `.deb`** from GitHub Releases and `apt-get
install`s it. The `.deb`'s `postinstall` drops `/usr/bin/node-stats`, creates
the data dir `/var/lib/node-stats`, and enables the `node-stats` **systemd**
service on `:8080`. Updates run the binary's own `node-stats update` self-update
and restart the unit.

| Resource | Default |
|----------|---------|
| OS       | Debian 12 |
| CPU      | 1 core |
| RAM      | 1024 MB |
| Disk     | 4 GB |
| Port     | 8080 |
| Unprivileged | yes |

> **No auto-connect here.** Unlike the repo's `scripts/proxmox-lxc.sh`, this
> community-scripts version does **not** mint a PVE API token or auto-attach the
> Proxmox connector (creating a PVE user/token is a host side-effect the
> community-scripts reviewers generally don't want a `*-install.sh` to do).
> Users add the connector in the UI after setup (admin → Connectors). The
> backend *does* support env-based auto-attach (`NODE_STATS_PROXMOX_URL` +
> `NODE_STATS_PROXMOX_TOKEN_ID` + `NODE_STATS_PROXMOX_TOKEN_SECRET`), so a
> reviewer who wants it could wire token creation into `ct/node-stats.sh` (which
> runs on the host) — see [`docs/PROXMOX-LXC.md`](../../docs/PROXMOX-LXC.md).

## Submitting / testing the PR

The maintainers' contributor guide is the source of truth:
<https://github.com/community-scripts/ProxmoxVE/blob/main/.github/CONTRIBUTING.md>.
The short version:

1. **Fork** `community-scripts/ProxmoxVE` and create a branch.
2. Copy the files into the matching paths in your fork:
   - `ct/node-stats.sh`
   - `install/node-stats-install.sh`
   - `frontend/public/json/node-stats.json`  ← note: the JSON lives under
     `frontend/public/json/` in their repo (it is `json/node-stats.json` here).
3. **Test from your fork on a real Proxmox VE host.** `build.func` reads the
   org/branch it pulls from out of these env vars, so point them at your fork:
   ```bash
   # on the PVE host, as root
   export USER=<your-gh-user> BRANCH=<your-branch>
   bash -c "$(curl -fsSL https://raw.githubusercontent.com/<your-gh-user>/ProxmoxVE/<your-branch>/ct/node-stats.sh)"
   ```
   (If the variable names drift, just edit the `build.func` URL at the top of
   `ct/node-stats.sh` to point at your fork while testing, then revert before
   the PR.)
4. Run their linters locally — `shellcheck` + `shfmt -d` on both scripts, and
   validate the JSON. CI runs the same checks plus a schema check on the JSON.
5. Open the PR against `main`. Fill in the template; a maintainer will spin up
   the container from your branch to verify.

### Notes for the reviewer / things to double-check before opening the PR

- **`categories`**: `9` is *Monitoring* in their category map — confirm against
  the current list and adjust if it changed.
- **`logo`**: points at this repo's `frontend/favicon/favicon.svg`. They may ask
  you to add an icon to their `misc/` / use a selfh.st-hosted one instead.
- **`date_created`**: set to the day you open the PR.
- **Author/Source headers** already credit `LastSkywalkerER` and this repo.
