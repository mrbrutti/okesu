# Okesu OCI smoke-test Terraform

Brings up the full Phase 8 production-shape stack on OCI for time-boxed
validation runs: Postgres + Streaming + Cache + Object Storage + OKE
(ClickHouse) + 8 daemon VMs. Tear it all down with one command.

> **Status:** unvalidated. The HCL follows the OCI provider docs but
> has not run end-to-end against a tenancy yet. Treat the first apply
> as the validation pass and capture findings in
> [`docs/oci-validation.md`](../../../docs/oci-validation.md).

## Quick reference

```bash
# 0. Drop tenancy_ocid, user_ocid, compartment_ocid, ssh_public_key into
#    terraform.tfvars (see ../../../docs/oci-validation.md §Phase 0).

make init
make plan-network    && make apply-network
make plan-managed    && make apply-managed
make plan-oke        && make apply-oke
make plan-fleet      && make apply-fleet
# … run smoke tests …
make destroy
```

Every `apply-*` requires interactive confirmation **after** showing
the plan — no surprise spend.

## Layout

```
deploy/oci/terraform/
  main.tf              orchestrates the modules; only conditional logic
  variables.tf         tenancy_ocid, region, fleet_size, ssh_public_key
  outputs.tf           DSNs/endpoints ready to drop into cp.yaml
  versions.tf          provider pin
  Makefile             plan-* / apply-* targets per phase
  modules/
    network/           VCN, subnets, gateways, security lists  (free)
    db/                Database with PostgreSQL                ($0.10/hr)
    streaming/         OCI Streaming stream pool               ($0.001/MB)
    cache/             OCI Cache (Redis)                       ($0.05/hr)
    objectstorage/     bucket + Customer Secret Keys           (per-GB)
    oke/               OKE cluster + ClickHouse Helm           ($0.005/hr)
    fleet/             daemon VMs                              ($0.005/hr × N)
```

## Cost summary

Targeting ~$5/day for the full stack running. See the runbook for
itemized estimates.

## Why split into phase-specific apply targets

Each phase validates a specific Phase 8 port. If `make apply-managed`
surfaces a Postgres bug, you're not also paying for OKE while you fix
it. Bring up the cheapest layer first, validate, then escalate.

The Makefile uses `terraform plan -target=...` to scope each phase.
That's normally an anti-pattern (it skips dependency analysis), but
each module here is genuinely independent and rooted in the network
module — Terraform's targeting handles that correctly.
