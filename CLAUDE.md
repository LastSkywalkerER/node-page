@ARCHITECTURE.md

## Release workflow convention

All changes follow this flow: **test → build → verify the target feature and its end-to-end flow**,
then **push to `main` immediately tagged as `beta`**. Promotion to the **`stable`** line happens
**only on an explicit request from the user** — never automatically. So `main` is the beta channel by
default; stable is a manual, on-demand promotion.
