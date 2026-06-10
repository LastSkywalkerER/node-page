# Discovering an application's original compose file

How node-stats (and tools like Portainer/Dokploy) locate the **source**
`docker-compose.yml` for a running stack, and how to verify the pattern on a host.

## The pattern: compose labels

Docker Compose **v2** stamps every container it creates with labels that point
back at the project and its source files:

| Label | Meaning |
|-------|---------|
| `com.docker.compose.project` | project name (the grouping key) |
| `com.docker.compose.service` | service name within the project |
| `com.docker.compose.project.config_files` | comma-separated **absolute host paths** of the compose file(s) used |
| `com.docker.compose.project.working_dir` | the directory `docker compose` ran in |

`config_files` is the authoritative source-of-truth: it is the exact path of the
file the operator (or orchestrator) deployed. node-stats reads these labels in
[`internal/metrics/docker/compose.go`](../internal/metrics/docker/compose.go)
(`ReadRealCompose`) and shows the real YAML in the application's **Composition**
tab when the path is reachable from the node-stats process.

## How other tools do it

- **Portainer** owns the stacks it deploys, so it stores them inside its own data
  volume: `/data/compose/<stackID>/docker-compose.yml` (plus a `.env`). It can
  always read them because they live in its container. For stacks it did *not*
  create, it falls back to the same compose labels above.
- **Dokploy** writes generated compose files to the host under
  `/etc/dokploy/compose/<project>/docker-compose.yml` (and
  `/etc/dokploy/applications/...`), then `docker compose up` stamps the
  `config_files` label with that path.

The common denominator is the `config_files` label — read it, then read the file
it names.

## The containerisation catch

The path in `config_files` is a **host** path. A node-stats running:

- **natively** on the host → can read it directly.
- **in a container** → can only read it if that host path is bind-mounted in.
  node-stats also tries the path under `HOST_ROOT` (e.g. host root at `/host` →
  reads `/host/etc/dokploy/compose/.../docker-compose.yml`). It also falls back to
  the `com.docker.compose.project.working_dir` label's canonical compose file when
  `config_files` is absent. **The bundled deployment already mounts `- /:/host:ro`
  with `HOST_ROOT=/host`** (see `docker-compose.yml` / `install/docker-compose.base.yml`),
  so the **Real YAML** view works for peer apps out of the box. Only override if you
  run a custom compose without that mount (then add `-v /:/host:ro` or, more
  narrowly, `-v /etc/dokploy:/etc/dokploy:ro`).

All of this keys off the standard compose labels — there is **no orchestrator- or
project-specific path hardcoding**.

## Verify the pattern on your host

Run these on the machine where the stack lives.

```sh
# 1. Which compose file(s) back a given container?
docker inspect <container> \
  --format '{{ index .Config.Labels "com.docker.compose.project.config_files" }}'

# 2. Sweep every running container → project, service, and its compose file path
docker ps -q | while read c; do
  docker inspect "$c" --format \
    '{{ index .Config.Labels "com.docker.compose.project"}}	{{ index .Config.Labels "com.docker.compose.service"}}	{{ index .Config.Labels "com.docker.compose.project.config_files"}}'
done | sort -u

# 3. All compose files for one project (e.g. board-plane-mvyzj3)
docker ps --filter "label=com.docker.compose.project=board-plane-mvyzj3" -q \
  | xargs -I{} docker inspect {} \
      --format '{{ index .Config.Labels "com.docker.compose.project.config_files" }}' \
  | tr ',' '\n' | sort -u

# 4. Where Dokploy keeps generated compose files
ls -la /etc/dokploy/compose/ 2>/dev/null
find /etc/dokploy -name 'docker-compose*.yml' 2>/dev/null

# 5. (Portainer hosts) where Portainer stores stack compose files
docker volume inspect portainer_data --format '{{ .Mountpoint }}' 2>/dev/null
find /var/lib/docker/volumes/portainer_data/_data/compose -name 'docker-compose.yml' 2>/dev/null
```

Once `config_files` resolves to a real path, bind-mount that path (or the host
root) into the node-stats container and the **Composition → Real YAML** view will
render the original file instead of the reconstructed one.
