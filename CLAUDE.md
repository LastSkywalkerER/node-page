@ARCHITECTURE.md

## Release workflow convention

All changes follow this flow: **test → build → verify the target feature and its end-to-end flow**,
then **push to `main` immediately tagged as `beta`**. Promotion to the **`stable`** line happens
**only on an explicit request from the user** — never automatically. So `main` is the beta channel by
default; stable is a manual, on-demand promotion.

## Caching convention — keep resident memory low

Regenerable caches (e.g. resolved app-icon bytes) are cached **only in the database**, never in a
per-process in-RAM cache. The app's resident memory must stay small (this is a recurring priority);
prefer a DB-backed cache table (dialect-agnostic: BLOB on SQLite, BYTEA on Postgres) over holding
bytes in process memory. Lightweight lookup helpers (e.g. the selfh.st slug index) may stay in RAM.
Such caches are excluded from Raft snapshots and the DB-switch dump.
