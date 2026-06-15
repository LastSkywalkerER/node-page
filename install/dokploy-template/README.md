# Dokploy template for node-stats

This is a ready-to-contribute [Dokploy template](https://github.com/Dokploy/templates):
`template.toml` generates the Postgres password and JWT secrets at create time
and wires the domain, so the app ships fully configured — the setup wizard
only asks for the admin account.

## PostgreSQL vs SQLite

- `docker-compose.yml` — **PostgreSQL** (default): a managed `db` service.
- `docker-compose.sqlite.yml` — **SQLite**: single container, the db file lives in
  the `node-stats-data` volume; no `db` service and `DB_PASSWORD` is unused.

Both default to `:latest`; set `NODE_STATS_IMAGE=ghcr.io/lastskywalkerer/node-page:beta`
in the Environment tab to follow the beta channel.

## Use it on your own dokploy (today)

1. Create a Compose app, paste `docker-compose.yml` (PostgreSQL) **or**
   `docker-compose.sqlite.yml` (SQLite) as Raw.
2. In the Environment tab add the secrets `template.toml` would generate:
   `JWT_SECRET`, `REFRESH_SECRET` (and `DB_PASSWORD` for the PostgreSQL variant) —
   any long random strings.
3. Domains → Add: service `node-stats`, container port `9090`, HTTPS on.

## Publish to the dokploy template store

Fork `Dokploy/templates`, copy this folder to `blueprints/node-stats/`, add a
logo + an entry in `meta.json`, open a PR — the repo CI deploys a preview
automatically.
