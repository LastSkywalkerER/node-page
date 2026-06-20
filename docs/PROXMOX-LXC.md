# Deploy node-stats in a Proxmox LXC

Run node-stats as a tiny, always-on Debian **LXC** on your Proxmox VE host — the
same "one command on the hypervisor" experience as the
[community-scripts.org](https://community-scripts.org) (Proxmox VE Helper-Scripts)
catalog. The container installs the **native single binary** (it reads `/proc`
directly, so you get real metrics) as a `systemd` service.

There are two ways in:

1. **[`scripts/proxmox-lxc.sh`](../scripts/proxmox-lxc.sh)** — a self-contained
   script in *this* repo. Works today, no external dependencies. **Recommended.**
2. The **community-scripts.org listing** — once the
   [submission kit](../contrib/proxmox-community-scripts/) is merged into the
   `community-scripts/ProxmoxVE` repo, node-stats shows up in their website/menu.

---

## 1. Self-contained script (recommended)

**On the Proxmox VE host** (the hypervisor itself, as `root` — not inside a VM):

```bash
bash -c "$(curl -fsSL https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/proxmox-lxc.sh)"
```

It picks the next free CTID, downloads the Debian 12 LXC template if needed,
creates an **unprivileged** container, installs the latest node-stats release
`.deb` inside, **auto-attaches it to this Proxmox host** (see below), and starts
it. When it finishes it prints:

```
http://<container-ip>:8080
```

Open that in a browser to finish the setup wizard (create the admin, pick SQLite
or PostgreSQL). The Proxmox host and every VM/LXC are already there.

### Auto-attach to the Proxmox host (built in)

Because the script runs **on the PVE host as root**, it can wire the connector
for you — so the app "sees Proxmox" with zero clicks:

1. It mints a dedicated **read-only** API token via `pveum`
   (user `node-stats@pam`, role `PVEAuditor` on `/`, token `nodestats-c<CTID>`,
   `privsep 0`). `PVEAuditor` can read all nodes/guests/storage but **cannot
   control anything**.
2. It pre-seeds stable `JWT_SECRET`/`REFRESH_SECRET` and writes the Proxmox URL
   (`https://<host-ip>:8006`) + token into the container's
   `/var/lib/node-stats/.env`.
3. On first boot node-stats reads `NODE_STATS_PROXMOX_*`, validates the token,
   encrypts it, and creates the connector — then the poller renders the
   hypervisor + guests. (Backend: `connectors.BootstrapProxmoxFromEnv`.)

It's **idempotent and safe**: the app skips bootstrap if a Proxmox connector
already exists. On a re-create the script re-issues the per-container token
(`nodestats-c<CTID>`) — PVE tokens outlive the container, and a freshly created
container has an empty DB, so rotating the secret can't orphan anything. Every
failure (no `pveum`, unreachable API, …) degrades gracefully to "add the
connector in the UI" — the install still succeeds.

> Why pre-seed the secrets? The connector token is encrypted under a key derived
> from `JWT_SECRET`. Pinning it before the setup wizard runs keeps the token
> decryptable across the wizard and future restarts. The wizard reuses the
> pre-seeded values, so you never type them.

> **Requires a release that includes auto-connect.** The native install pulls
> the latest **GitHub Release** `.deb`. Auto-connect (`BootstrapProxmoxFromEnv`)
> landed after `v0.7.9`, so it only kicks in once the installed release contains
> it. If you're on an older release the script still wires everything correctly,
> but the binary ignores the `NODE_STATS_PROXMOX_*` env — just add the connector
> in the UI (the script already saved a working token in the container's
> `/var/lib/node-stats/.env`; copy the `NODE_STATS_PROXMOX_*` values into the
> Connectors form). Pin a specific build with `NODE_STATS_VERSION=vX.Y.Z`.

| Auto-connect knob | Default | Meaning |
|-------------------|---------|---------|
| `PROXMOX_AUTOCONNECT` | `1` | `0`/`false`/`off` skips the whole auto-attach. |
| `PROXMOX_URL` | `https://<host-ip>:8006` | Override the Proxmox API URL the container uses. |
| `PROXMOX_USER` | `node-stats@pam` | PVE user the token belongs to. |
| `PROXMOX_ROLE` | `PVEAuditor` | Role granted on `/` (read-only). |
| `PROXMOX_TOKEN_NAME` | `nodestats-c<CTID>` | Token id name (per-container, avoids clashes). |
| `PROXMOX_TOKEN_ID` / `PROXMOX_TOKEN_SECRET` | — | Bring your **own** token; the script then won't touch PVE auth. |
| `PROXMOX_SKIP_TLS_VERIFY` | `1` | PVE ships a self-signed cert; set `0` if you front it with a trusted cert. |

### Defaults

| Setting | Default | Env override |
|---------|---------|--------------|
| CTID | next free id | `CTID` |
| Hostname | `node-stats` | `CT_HOSTNAME` |
| OS | Debian 12 | — |
| CPU | 1 core | `CORE_COUNT` |
| RAM / swap | 1024 MB | `RAM_SIZE` |
| Disk | 4 GB | `DISK_SIZE` |
| Bridge | `vmbr0` | `BRIDGE` |
| Network | DHCP | `NET` (`dhcp` or a CIDR like `192.168.1.50/24`), `GATEWAY`, `DNS` |
| Unprivileged | yes | `UNPRIVILEGED` (`1`/`0`) |
| Nesting | on | `ENABLE_NESTING` |
| Start on boot | yes | `START_ON_BOOT` |
| HTTP port | 8080 | `HTTP_PORT` |
| Template / rootfs storage | first that supports it | `TEMPLATE_STORAGE`, `CONTAINER_STORAGE` |
| Release | latest | `NODE_STATS_VERSION=vX.Y.Z` |

### Examples

Static IP, 2 cores, 2 GB RAM, on the `local-lvm` storage:

```bash
CTID=151 NET=192.168.1.50/24 GATEWAY=192.168.1.1 \
CORE_COUNT=2 RAM_SIZE=2048 CONTAINER_STORAGE=local-lvm \
bash -c "$(curl -fsSL https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/proxmox-lxc.sh)"
```

Interactive whiptail wizard (prompts for CTID / resources / network):

```bash
ADVANCED=1 bash -c "$(curl -fsSL https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/proxmox-lxc.sh)"
```

See the full apt/pct output (useful when something fails):

```bash
VERBOSE=1 bash -c "$(curl -fsSL https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/proxmox-lxc.sh)"
```

### Day-2 management

The `update` / `uninstall` subcommands take a CTID argument, so download the
script once and run it directly (passing args cleanly):

```bash
curl -fsSL https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/proxmox-lxc.sh -o /tmp/node-stats-lxc.sh

# self-update node-stats inside the container (newest release + restart)
bash /tmp/node-stats-lxc.sh update 151

# switch this container to the beta channel and update to the latest prerelease
# (persisted — later `update` runs keep following beta; use stable to go back)
NODE_STATS_CHANNEL=beta bash /tmp/node-stats-lxc.sh update 151

# (re)wire Proxmox auto-connect into an existing container — no recreate.
# Rotates the token, writes the creds, restarts. Keeps an existing JWT secret
# (won't log you out). Use it after upgrading to a release with auto-connect,
# or to attach a container that was set up before it was available.
bash /tmp/node-stats-lxc.sh reconnect 151

# stop + destroy the container (asks for confirmation)
bash /tmp/node-stats-lxc.sh uninstall 151

# follow logs / open a shell
pct exec 151 -- journalctl -u node-stats -f
pct enter 151
```

> If you cloned the repo, just run `bash scripts/proxmox-lxc.sh update 151`.

You can also flip on **auto-update** inside the app (admin → settings): the
native install applies updates with `node-stats update` on its own.

---

## Monitoring the whole Proxmox host (not just the container)

The container itself shows up as one machine card (lxcfs scopes its CPU/RAM/disk
to the container). The **Proxmox host and every VM/LXC** come from the connector
— which this script **already wired for you** (see *Auto-attach* above), so by
default there's nothing to do here.

If you ran with `PROXMOX_AUTOCONNECT=0`, or auto-attach failed, add it by hand:

1. In Proxmox: create an API token (`Datacenter → Permissions → API Tokens`)
   with at least `PVEAuditor` on `/`.
2. In node-stats: **admin → Connectors → Proxmox**, paste the host URL + token.

The leader polls the PVE API every 10 s and renders the hypervisor plus all
guests as machine cards — agent-less guests get metrics straight from the API,
and guests that *do* run a node-stats agent are matched by NIC MAC so nothing is
duplicated. Full details in [PROXMOX.md](PROXMOX.md). There's an equivalent
**Proxmox Backup Server** connector too.

> Prefer the container to read the **host's** `/proc`/`/sys` directly instead of
> using the connector? Run it **privileged** (`UNPRIVILEGED=0`) and bind-mount
> the host paths into the container, then set `HOST_PROC`/`HOST_SYS` on the
> service (see the env table in [ARCHITECTURE.md](../ARCHITECTURE.md)). The
> connector is the cleaner, safer route for most setups.

---

## 2. Getting it onto community-scripts.org

The files needed to list node-stats on community-scripts.org live in
[`contrib/proxmox-community-scripts/`](../contrib/proxmox-community-scripts/)
(`ct/`, `install/`, `json/`) with a step-by-step submission guide in that
folder's [README](../contrib/proxmox-community-scripts/README.md). In short: fork
`community-scripts/ProxmoxVE`, drop the files into the matching paths, test from
your fork on a real PVE host, run their linters, and open a PR.

Those framework scripts and `scripts/proxmox-lxc.sh` install node-stats the
**same way** (release `.deb` → systemd on `:8080`), so behaviour is identical
whichever entry point you use.

---

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `this is not a Proxmox VE host` | Run it on the hypervisor (where `pct`/`pveam` live), not inside a VM/LXC. |
| `no storage with 'rootdir'/'vztmpl' content` | Pick storages explicitly: `CONTAINER_STORAGE=local-lvm TEMPLATE_STORAGE=local`. |
| `CTID ... already exists` | Choose another: `CTID=<n>`. |
| container has no IP / install fails | Re-run with `VERBOSE=1`; check the bridge (`BRIDGE=`) and DHCP, or set a static `NET=`. |
| page won't load | `pct exec <id> -- systemctl status node-stats`; logs via `journalctl -u node-stats`. |
