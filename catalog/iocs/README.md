# IOC Catalog

Curated indicators of compromise, version-controlled alongside the project.

Each `.yaml` / `.yml` file under `catalog/iocs/` contributes entries to
the CP's IOC table on startup (and on `SIGHUP`). Federated parent ↔
child like agent files.

See `example.yaml.template` for the file schema and
`docs/superpowers/specs/2026-04-29-threatcaddy-borrows-phasing-design.md`
for the design.

## Authoring

Drop a `.yaml` (or `.yml`) file in this directory with the schema shown
in `example.yaml.template`. The template ships with the `.template`
suffix so the loader (which only matches `.yaml` / `.yml`) skips it —
copy or rename it to `*.yaml` once you've replaced the placeholder
indicators with real curated entries.

The CP loads the directory once at boot and again on every `SIGHUP`.
Send `kill -HUP $(pgrep okesu-cp)` after editing files to pick up
changes without a restart.

## Removal semantics (v1)

Deleting an entry from a YAML file (or removing a YAML file) does NOT
delete the corresponding row from the `iocs` table. Curated rows
persist until they're manually cleared or until a future phase adds
removal tracking. For now: treat catalog edits as additive / amending,
not pruning.
