# Putting node-stats behind a reverse proxy (NPM / Traefik)

The app is proxy-friendly out of the box:

- the SSE stream (`/api/v1/stream`) sends `X-Accel-Buffering: no` and
  `Cache-Control: no-cache`, so nginx-based proxies (incl. Nginx Proxy
  Manager) do not buffer it; events tick every ~5 s, well under default
  read timeouts;
- auth cookies are host-only `SameSite=Lax` — they work unchanged when the
  app is served through a proxy on its own (sub)domain;
- the frontend calls the API with relative URLs (`/api/v1/...`), so any
  host-based route works without path rewriting.

Two facts to keep in mind:

1. **Inside the container the app always listens on `:9090`.** The published
   host port (`NODE_STATS_PORT` in the stack `.env`, default `9090`) may
   differ — the installer auto-picks a free one when `9090` is taken. Proxy
   over a shared docker network → target port **9090**; proxy via the host →
   target the port from `.env`.
2. **Put your proxy wiring in `docker-compose.override.yml`** in the stack
   dir (`/opt/node-stats` for root installs). That file is installer-owned —
   the controller regenerates `docker-compose.yml` but **never touches the
   override**, and Docker Compose merges the two automatically. Anything you
   add to `docker-compose.yml` itself would be lost on the next wizard
   change or auto-update.

---

## Option A — shared docker network (recommended)

Works for NPM and Traefik alike: both reach the app container directly by
its compose service name (`node-stats`), no published port needed.

```bash
docker network create proxy   # once; skip if your proxy already has one
docker network connect proxy nginxproxymanager   # attach your existing NPM
```

Append to `docker-compose.override.yml` in the stack dir (keep whatever is
already there — on Linux that's `pid: host` / `ipc: host`):

```yaml
services:
  node-stats:
    networks:
      - default
      - proxy

networks:
  proxy:
    external: true
```

Apply: `cd /opt/node-stats && docker compose up -d`.

**Nginx Proxy Manager** → Add Proxy Host:

| Field | Value |
|---|---|
| Domain Names | `stats.example.com` |
| Scheme | `http` |
| Forward Hostname / IP | `node-stats` |
| Forward Port | `9090` (the in-container port, always 9090) |
| Websockets Support | optional (the app uses SSE, not WS — works either way) |

Request an SSL cert on the host as usual.

## Option B — via the published host port (no network changes)

Point the proxy at the host instead of the container. In NPM set Forward
Hostname to the host's LAN IP (or `172.17.0.1`, the docker bridge gateway)
and Forward Port to the value of `NODE_STATS_PORT` from the stack `.env`
(default `9090`; check it — the installer may have picked e.g. `9091`).

```bash
grep NODE_STATS_PORT /opt/node-stats/.env
```

## Option C — Traefik (docker provider, labels)

Same shared network as Option A, plus labels in the override:

```yaml
services:
  node-stats:
    networks:
      - default
      - proxy
    labels:
      - traefik.enable=true
      - traefik.docker.network=proxy
      - traefik.http.routers.node-stats.rule=Host(`stats.example.com`)
      - traefik.http.routers.node-stats.entrypoints=websecure
      - traefik.http.routers.node-stats.tls.certresolver=le
      - traefik.http.services.node-stats.loadbalancer.server.port=9090

networks:
  proxy:
    external: true
```

`server.port` is the **in-container** port — always `9090`, regardless of
what the installer published on the host.

With Traefik's **file provider** instead, point the service URL at the host:
`url: http://<host-ip>:<NODE_STATS_PORT from .env>`. Bonus: bind-mount the
dynamic-config dir into the app and set `TRAEFIK_DYNAMIC_DIR` so the
Applications view picks up public URLs — but note that setting
`TRAEFIK_DYNAMIC_DIR` flips the node into *managed-externally* mode
(disables the controller's compose mutation), which is meant for
orchestrator-owned deployments (dokploy etc.), not for the standard
installer stack.

---

## HTTPS notes

Once the proxy terminates TLS, set in the stack dir's `.env.agent`:

```
COOKIE_SECURE=true
```

then `docker compose restart node-stats`. Auth cookies get the `Secure`
flag; everything else (SSE, relative API calls) needs no changes.

## Cluster / Raft caveat

Only the HTTP API (`:9090`) goes through the proxy. **Raft replication is
raw TCP** (default `:7000`) — an HTTP proxy like NPM cannot forward it;
cluster peers must reach each other's Raft port directly (LAN, VPN/NetBird,
or a TCP-level forward). What you *can* route through the proxy is the
HTTP side of clustering: the node's public URL
(`RAFT_ADVERTISE_PUBLIC_URL`) lets join/forward requests and the Nodes
list use the proxied `https://stats.example.com`. **The setup wizard
detects it automatically**: when you open setup (or the join form)
through a domain rather than an IP, the browser's address is prefilled
as the public URL — adjust or clear it under *Advanced*. The admin
Nodes → Cluster form goes further and TCP-probes the candidates from
the server, showing green/amber reachability chips.
