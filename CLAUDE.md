@ARCHITECTURE.md

## Release workflow convention

All changes follow this flow: **test → build → verify the target feature and its end-to-end flow**,
then **commit and push straight to `main` — no extra feature/dev branches, no PR** — immediately tagged
as `beta`. Promotion to the **`stable`** line happens **only on an explicit request from the user** —
never automatically. So `main` is the beta channel by default; stable is a manual, on-demand promotion.

## Caching convention — keep resident memory low

Regenerable caches (e.g. resolved app-icon bytes) are cached **only in the database**, never in a
per-process in-RAM cache. The app's resident memory must stay small (this is a recurring priority);
prefer a DB-backed cache table (dialect-agnostic: BLOB on SQLite, BYTEA on Postgres) over holding
bytes in process memory. Lightweight lookup helpers (e.g. the selfh.st slug index) may stay in RAM.
Such caches are excluded from Raft snapshots and the DB-switch dump.

## UI convention — no native browser dialogs

Never use `window.confirm` / `window.alert` / `window.prompt`. There is a reusable in-app
confirmation dialog: **`confirmDialog(options)`** (`frontend/src/shared/lib/confirmDialog.ts`),
rendered by **`<ConfirmDialogHost/>`** (mounted once at the app root in `App.tsx`). It's imperative
and promise-based — `const { confirmed, checked } = await confirmDialog({ title, description,
variant: 'destructive', confirmText, checkbox })` — works from any handler, supports an optional
checkbox (for two-step confirms like "also remove hosts?"), and closes on Escape / click-away. Use it
for every confirmation/destructive prompt (node/connector removal, settings applies, raft resets, …).
Popovers should also dismiss on outside-click + Escape (document-level listener, not just an overlay
div — header `backdrop-blur` creates a stacking context that traps overlay z-index).
