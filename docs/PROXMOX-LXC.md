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
`.deb` inside, and starts it. When it finishes it prints:

```
http://<container-ip>:8080
```

Open that in a browser to finish the setup wizard (create the admin, pick SQLite
or PostgreSQL).

### Defaults

| Setting | Default | Env override |
|---------|---------|--------------|
| CTID | next free id | `CTID` |
| Hostname | `node-stats` | `HOSTNAME` |
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
ADVANCED=1 bash -c "$(curl -fsSL .../scripts/proxmox-lxc.sh)"
```

See the full apt/pct output (useful when something fails):

```bash
VERBOSE=1 bash -c "$(curl -fsSL .../scripts/proxmox-lxc.sh)"
```

### Day-2 management

The `update` / `uninstall` subcommands take a CTID argument, so download the
script once and run it directly (passing args cleanly):

```bash
curl -fsSL https://raw.githubusercontent.com/LastSkywalkerER/node-page/main/scripts/proxmox-lxc.sh -o /tmp/node-stats-lxc.sh

# self-update node-stats inside the container (newest release + restart)
bash /tmp/node-stats-lxc.sh update 151

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

By default the LXC monitors **itself** — its own CPU/RAM/disk (lxcfs scopes those
to the container). That is the standard helper-script model: a lightweight,
isolated dashboard appliance.

To see the **Proxmox host and every VM/LXC**, use node-stats' built-in
**Proxmox connector** after setup:

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
