# OCI smoke validation

A two-pass checklist that exercises `make oci-deploy` end-to-end against
a real OCI tenancy. Run in a dedicated, throwaway compartment.

## Prerequisites

- `oci` CLI authenticated (`oci session authenticate` for SSO, or API key in `~/.oci/config`)
- `terraform >= 1.6`
- An SSH keypair (`~/.ssh/id_ed25519` or similar)
- An Anthropic API key
- A target OCI compartment OCID + region

## Pass 1 — Standalone

- [ ] `cp deploy/oci/.env.oci.example .env.oci` and set `ANTHROPIC_API_KEY`
- [ ] `cp deploy/oci/modes/standalone.tfvars.example deploy/oci/terraform/standalone.tfvars` and fill in (compartment_ocid, region, ssh_public_key, etc.)
- [ ] `make oci-test` — render goldens + terraform validate pass
- [ ] `make oci-build`
- [ ] `make oci-deploy MODE=standalone`
- [ ] Open `https://<cp_public_ip>:8443` (printed at the end of deploy)
- [ ] Log in with the admin password from `~/.okesu-secrets/cp/admin-password`
- [ ] Settings → Deploy → Add a daemon node (one of the fleet VMs); confirm it heartbeats
- [ ] Run an investigation; confirm the agent's Anthropic call succeeds (i.e., `ANTHROPIC_API_KEY` reached the CP)
- [ ] `make oci-destroy MODE=standalone`

## Pass 2 — Parent + Child federation

This pass tests federated catalog data flowing from a child CP into a
parent's S3 bucket.

- [ ] In `~/okesu-parent/`, deploy parent: `make -C ~/okesu oci-deploy MODE=parent OCI_DIR=$PWD/terraform`
- [ ] `make -C ~/okesu oci-print MODE=parent OCI_DIR=$PWD/terraform` — copy the federation outputs
- [ ] In `~/okesu-child/`, prepare:
  - Copy `deploy/oci/terraform/` from the repo
  - Set `OCI_DIR=$PWD/terraform`
  - Fill in `terraform/child.tfvars` (paste federation bucket/endpoint/region/access_key)
  - Fill in `.env.oci` (paste `PARENT_FEDERATION_TOKEN`, `PARENT_FEDERATION_SECRET_KEY`, plus your own `ANTHROPIC_API_KEY`)
- [ ] `make -C ~/okesu oci-deploy MODE=child OCI_DIR=$PWD/terraform`
- [ ] On the parent CP UI: Catalog → Federated CPs → confirm the child appears and a federated request flows through
- [ ] `make -C ~/okesu oci-destroy MODE=child OCI_DIR=$PWD/terraform`
- [ ] `make -C ~/okesu oci-destroy MODE=parent`

## Notes

- A full standalone pass takes ~30 minutes (Postgres provisioning is the long pole at ~15 min).
- Use a dedicated compartment so `terraform destroy` is unambiguous.
- If any step fails, capture `journalctl -u okesu-cp --no-pager -n 200` from the CP VM and the failing step's `make` output.
- The Makefile retries SSH for ~2 minutes (12× 10s) and the healthcheck for ~90s (18× 5s). If the SSH retry exhausts, cloud-init may have failed — log into the VM and check `/var/log/cloud-init-output.log`.

## Cost note

Provisioned resources (per deployment, approximate):
- 2× OCI Compute (E4.Flex.1.16GB) — cp-vm + ch-vm — paid per-OCPU-hour
- 1× OCI Database with PostgreSQL — smallest tier ~$0.10/hr
- 1× OCI Cache (Redis) — 2 GB single-node ~$0.05/hr
- N× fleet VMs — first 2 are Always Free; additional ~$0.005/hr each
- OCI Streaming + Object Storage — pennies at smoke volumes

**A standalone smoke that runs for an hour costs ~$0.20 in OCI charges.**
Always destroy after smoke runs.
