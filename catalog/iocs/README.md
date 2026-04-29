# IOC Catalog

Curated indicators of compromise, version-controlled alongside the project.

Each YAML file under `catalog/iocs/` contributes entries to the CP's IOC
table on startup (and on `SIGHUP`). Federated parent ↔ child like
agent files.

See `example.yaml` for the file schema and
`docs/superpowers/specs/2026-04-29-threatcaddy-borrows-phasing-design.md`
for the design.
