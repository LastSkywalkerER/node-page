# Dokploy template for node-stats

This is a ready-to-contribute [Dokploy template](https://github.com/Dokploy/templates):
`template.toml` generates the Postgres password and JWT secrets at create time
and wires the domain, so the app ships fully configured — the setup wizard
only asks for the admin account.

## Use it on your own dokploy (today)

1. Create a Compose app, paste `docker-compose.yml` (Raw type).
2. In the Environment tab add the three values `template.toml` would generate:
   `DB_PASSWORD`, `JWT_SECRET`, `REFRESH_SECRET` (any long random strings).
3. Domains → Add: service `node-stats`, container port `9090`, HTTPS on.

## Publish to the dokploy template store

Fork `Dokploy/templates`, copy this folder to `blueprints/node-stats/`, add a
logo + an entry in `meta.json`, open a PR — the repo CI deploys a preview
automatically.
