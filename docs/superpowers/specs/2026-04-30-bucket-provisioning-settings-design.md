# Bucket Provisioning in Settings — Design Spec

**Date:** 2026-04-30
**Status:** Approved (pending user review of this document)

## Goal

Replace the broken "Add bucket" link in `Settings → Cloud → Object storage buckets` (which navigates to `/nodes`) with a dedicated wizard native to Settings. The wizard mirrors the established Add-CP UX: pick a configured cloud provider, then either discover existing buckets or create a new one — all via SDK calls using the credentials we already have. Also add edit / delete actions for existing buckets that have been missing.

## Background

The control plane stores object-storage configuration in `transport_configs`. The current "Add bucket" surface is a manual form (operator types `bucket`, `endpoint`, `access_key`, `secret_key`, etc.) accessed via `Nodes → Add Node → S3 dead-drop`. Settings → Cloud has a read-only summary of the same data with an "Add bucket" button that mistakenly deep-links to `/nodes` (`web/src/pages/settings/Cloud.tsx:211`).

The Add-CP wizard already established the canonical "use configured cloud credentials + SDK" pattern (`controlplane/cpprovision/`). The `Provisioner` interface today only handles VM lifecycle (`Launch` / `Destroy`) — it has no bucket-related methods. Cloud credentials are stored in `cloud_credentials` (referenced by id from Add-CP) and the supported set is `db.AllowedCloudKinds = [oci, aws, gcp, azure, digitalocean]`. There are no edit/delete endpoints on `transport_configs` today, only `POST` (create) and `GET` (list).

This spec covers a new dedicated wizard in Settings that delivers three things:
1. Auto-provisioning via configured cloud credentials (AWS, OCI, MinIO).
2. A preserved manual-entry path for on-prem / third-party S3-compatible services without an Okesu credential.
3. Native edit + delete on existing transport_configs.

## Use cases addressed (priority order)

1. **Add a new bucket on top of credentials we already have** (primary). Operator has already configured an OCI tenancy or AWS account in Settings → Cloud; they expect to provision (or pick) a bucket in that account from Settings without leaving the page or re-entering keys.
2. **Discover an existing bucket** (peer of #1). Some operators have a security/ops separation where buckets are created upstream; the wizard lists existing buckets in the account and lets the operator pick one.
3. **Manual entry for non-configured providers** (preserved). MinIO admin who hasn't added a credential, on-prem object storage, or third-party S3-compatible service: the operator types every field by hand. Same shape as today.
4. **Edit + delete existing buckets** (new — fills the CRUD gap). Display name, scanner-interval, fleet-key regeneration are editable; bucket / endpoint / access keys are immutable identity. Delete is guarded against in-use references.

## Architecture

Three layers, mirroring how Add-CP is wired today.

### Backend — `BucketProvisioner` interface

New file `controlplane/cpprovision/bucket_provisioner.go`:

```go
type BucketProvisioner interface {
    Cloud() string  // "aws" | "oci" | "minio"

    ListBuckets(ctx context.Context, creds CredentialPayload, region string) ([]BucketInfo, error)
    EnsureBucket(ctx context.Context, creds CredentialPayload, name, region string) (*BucketInfo, error)

    // BucketAccessKeys returns S3-compatible access keys for the
    // bucket scoped to these credentials. AWS: returns the credential's
    // own access keys verbatim. OCI: auto-creates Customer Secret Keys
    // via OCI IAM. MinIO: returns the credential's stored keys.
    BucketAccessKeys(ctx context.Context, creds CredentialPayload) (accessKey, secretKey string, err error)
}

type BucketInfo struct {
    Name      string  // S3 bucket name
    Region    string
    Endpoint  string  // S3-compatible URL the operator's hosts will reach
}
```

`CredentialPayload` is the decrypted JSON blob already used by the existing `Provisioner.Launch(req.CredentialPayload []byte)` path; the new interface threads the same shape through.

**Implementations:**
- `controlplane/cpprovision/aws/bucket.go` — uses the AWS S3 SDK already vendored for the existing `aws.Provisioner`. `ListBuckets` is `s3.ListBuckets`; `EnsureBucket` is `s3.HeadBucket → CreateBucket if 404`. Endpoint is the standard `https://s3.<region>.amazonaws.com` form. `BucketAccessKeys` returns the credential's stored access_key + secret_key verbatim.
- `controlplane/cpprovision/oci/bucket.go` — uses the OCI Go SDK (already vendored). `ObjectStorageClient.ListBuckets`, `CreateBucket`. Endpoint is `https://<namespace>.compat.objectstorage.<region>.oraclecloud.com`. `BucketAccessKeys` calls OCI IAM's `CreateCustomerSecretKey` once per credential and caches the result. **Permissions note:** the stored OCI credential needs `inspect users` and `manage customer-secret-keys`. The wizard's pre-flight check surfaces a friendly error if missing.
- `controlplane/cpprovision/minio/bucket.go` — reuses the AWS S3 SDK with a custom endpoint URL pulled from the credential payload. MinIO is fully S3-compatible so no separate SDK needed. `BucketAccessKeys` returns the credential's stored keys verbatim.

Add `"minio"` to `db.AllowedCloudKinds`. The `cloud_credentials` row for MinIO carries `{ endpoint, access_key, secret_key }` (no region required — MinIO regions are deployment-local). The existing **Add Provider** form in `Settings → Cloud` gains a "MinIO" option alongside AWS/OCI; the form fields are `endpoint`, `access_key`, `secret_key`, `display_name`. This is a small frontend addition (one new branch in the existing Add-Provider modal) — not a separate page.

### Backend — HTTP endpoints

All in the cookie-auth viewer+ route group.

**New (provisioning):**
- `GET /api/buckets/cloud-providers` — returns the operator's `cloud_credentials` rows filtered to `cloud IN ('aws', 'oci', 'minio')`. Each row carries `{ id, cloud, display_name, region (optional) }` — same projection as Add-CP uses.
- `GET /api/buckets/discover?cloud_credential_id=N&region=R` — looks up the credential, dispatches to the matching `BucketProvisioner.ListBuckets`, returns `[]BucketInfo`. 502 if the SDK call fails (with the SDK's error in the body).
- `POST /api/buckets/provision` — body:
  ```json
  {
    "cloud_credential_id": 7,
    "region": "us-ashburn-1",
    "bucket_name": "okesu-fleet-prod",
    "mode": "create" | "discover",
    "display_name": "OCI Ashburn fleet bucket",
    "generate_fleet_keys": true,
    "scanner_interval_ms": 30000
  }
  ```
  Backend: looks up creds → if `mode=create` calls `EnsureBucket`, if `mode=discover` calls `ListBuckets` and validates the named bucket exists → calls `BucketAccessKeys` → writes a `transport_configs` row with the resolved endpoint + access keys → returns the row.

**New (CRUD on existing transport_configs):**
- `PATCH /api/transport-configs/{id}` — body `{ name?, scanner_interval_ms? }`. Identity fields (`bucket`, `endpoint`, `access_key`, `secret_key`, `cloud_credential_id`, `region`) are intentionally **immutable** — changing them would break enrollment packages and federated peers tied to the old identity. Returns the updated row. **Note:** key rotation (regenerating fleet keys post-creation) is intentionally **NOT** in this PATCH — it's a footgun (see Risks) and lands as a separate "Rotate keys" wizard later.
- `DELETE /api/transport-configs/{id}` — pre-flight check: query `cp_provisions`, `enrollment_packages`, `federation_peers` for any row referencing this id. If any references found, return `409 Conflict` with body listing the referencing resources. No `?force=true` — operator must clean up dependencies first. Soft-delete is not introduced (deleted rows are removed from `transport_configs`).

**Unchanged:** `POST /api/transport-configs` (manual entry), `GET /api/transport-configs`.

### Frontend — wizard + manage UI

**`web/src/components/AddBucketWizard.tsx`** (new) — replaces the broken `<Link to="/nodes">` "Add bucket" button.

Two-step modal:

**Step 1 — Source:**
- Radio: "Use configured cloud provider" (default if any AWS/OCI/MinIO credential exists) | "Manual entry"
- Configured-provider: dropdown of `cloud_credentials` rows. If empty, show a CTA "Add a cloud credential first →" linking to the existing Add Provider flow.
- Manual entry: jumps straight to step 2 with the existing form fields.

**Step 2 — Bucket (configured-provider path):**
- Region dropdown (provider-specific list — AWS regions vs OCI regions; MinIO shows only "default")
- Two tabs:
  - **Discover existing** — list from `GET /api/buckets/discover?...`. Click a bucket row to select it.
  - **Create new** — bucket name input with provider-specific validation (AWS: 3-63 chars lowercase, no underscores; OCI: more permissive; MinIO: same as AWS).
- Common fields below the tabs: display name, scanner interval (ms), "Generate fleet keypair" checkbox.
- Submit → `POST /api/buckets/provision` with `mode` set to `discover` or `create`.

**Step 2 — Bucket (manual-entry path):**
- Existing form fields verbatim: `bucket`, `endpoint`, `endpoint_internal`, `region`, `use_ssl`, `access_key`, `secret_key`, `display_name`, `generate_fleet_keys`, `scanner_interval_ms`.
- Submit → `POST /api/transport-configs` (existing endpoint, no behavior change).

On success: wizard closes, the `BucketsSection` list refreshes.

**Existing `BucketsSection` row actions:**

Each bucket row gains two new buttons (alongside the existing "Manage → /nodes" link, which stays for now since deeper node-level bucket operations live there):

- **Edit** — small modal with editable fields only (`display_name`, `scanner_interval_ms`). Calls `PATCH /api/transport-configs/{id}`. Key rotation is deliberately not exposed here — see Risks + Open follow-ups.
- **Delete** — confirmation dialog. On 409, shows the list of referencing resources from the response body with deep-links to the relevant pages and a "Remove dependencies first" message.

### Wire shapes

`web/src/api.ts` additions:

```ts
api.bucketProviders()                                          // GET /api/buckets/cloud-providers
api.bucketsDiscover(cloudCredentialID: number, region: string) // GET /api/buckets/discover
api.bucketsProvision(req: BucketProvisionReq)                  // POST /api/buckets/provision
api.transportConfigUpdate(id: number, patch: TransportConfigPatch)   // PATCH /api/transport-configs/{id}
api.transportConfigDelete(id: number)                          // DELETE /api/transport-configs/{id}
```

New types: `BucketInfo { name, region, endpoint }`, `BucketProvisionReq`, `TransportConfigPatch`, `TransportConfigDeleteConflict { referenced_by: { resource: string, id: number, name: string }[] }`.

## Out of scope (deferred)

- **MinIO embedded provisioning.** Spinning up MinIO as a managed sidecar / Docker container on the parent CP host is out of scope. MinIO is treated like any other configured provider: operator runs MinIO somewhere, registers it as a cloud credential, then provisions buckets in it via the wizard.
- **Editing identity fields** (`bucket`, `endpoint`, `access_key`, `secret_key`, `region`). Operators who need a different bucket delete + re-add. Identity-field edits would silently break enrollment packages and federated peers tied to the old identity.
- **Soft-delete** for transport_configs. v1 is a hard delete with the in-use guard.
- **Provider beyond AWS/OCI/MinIO.** GCP, Azure, DigitalOcean already exist as cloud credential kinds but are not in scope for this iteration's `BucketProvisioner` implementations. Adding them is a follow-up — interface is open.

## Out of scope (cut)

- **Force-delete with dependency cleanup.** No `?force=true` flag. Forcing delete would orphan transport_config_id references in enrollment packages, breaking offline child CPs that haven't yet enrolled. The 409 with referencing-resources list is the operator's path to clean up first.

## Testing

### Backend

- **DB migrations:** none — schema unchanged. New cloud kind `"minio"` is added to `db.AllowedCloudKinds` (Go-side whitelist), no SQL change needed.
- **Provisioner unit tests:** for each implementation (`aws/bucket_test.go`, `oci/bucket_test.go`, `minio/bucket_test.go`), mock the SDK client and assert `ListBuckets` / `EnsureBucket` invoke the right SDK methods with the right args. Cover happy path + a representative error (4xx, 5xx).
- **Handler tests:**
  - `GET /api/buckets/cloud-providers` filters to AWS/OCI/MinIO only.
  - `GET /api/buckets/discover` returns 502 when SDK fails; happy path returns the bucket list.
  - `POST /api/buckets/provision` for `mode=create` writes the correct transport_configs row; `mode=discover` validates the named bucket exists in `ListBuckets` output; missing credential → 400.
  - `PATCH /api/transport-configs/{id}` updates only the editable fields; identity fields in the request body are ignored (or 400 — pick one and stick to it; v1 ignores).
  - `DELETE /api/transport-configs/{id}` returns 409 with referencing-resources list when any of the three reference tables has a matching row; deletes cleanly otherwise.

### Frontend

- `npm run build` clean.
- Lab smoke (post-merge):
  - Configure an AWS credential in Settings → Cloud → Add Provider. Open the Add Bucket wizard. Pick the AWS credential. Region dropdown shows AWS regions. Discover lists existing buckets in the account; pick one — wizard closes, bucket appears in the Settings list.
  - Configure an OCI credential. Add Bucket → OCI → Create new → enter name → success: bucket created in OCI tenancy, transport_config row written with the auto-created Customer Secret Key.
  - Configure a MinIO credential (manual provider entry: endpoint + access/secret keys). Add Bucket → MinIO → Discover → pick existing.
  - Edit a bucket: change display name → save. Verify the list reflects the new name.
  - Delete a bucket that's referenced by a federated peer: confirm 409 dialog shows the peer with a link.
  - Delete a bucket with no references: confirm dialog → confirm → row gone.

## Risks

- **OCI Customer Secret Key permission gaps.** The operator's stored OCI credential may not have `manage customer-secret-keys` on the user. **Mitigation:** the wizard pre-flight-checks the credential by calling a low-impact OCI IAM endpoint (e.g., `ListCustomerSecretKeys` for the user) before showing the Create / Discover tabs. On 403, surface a copy-pasteable IAM policy snippet the operator can apply.
- **AWS bucket-creation race / global namespace.** S3 bucket names are globally unique across all AWS accounts. `EnsureBucket` may fail with `BucketAlreadyExists` even if the operator has never seen that name. **Mitigation:** surface the SDK error verbatim in the wizard's "Create new" tab; the operator picks a different name.
- **Key rotation is intentionally deferred.** Allowing post-creation key regeneration would let an operator silently break every deployed node + child CP currently writing to the bucket (re-enrollment is required after rotation). The v1 edit modal does NOT expose key rotation; rotation lands later as a dedicated "Rotate keys" wizard with explicit re-enrollment guidance + a list of affected resources. The PATCH endpoint accepts only `name` and `scanner_interval_ms`.

## Open follow-ups (post-v1)

- GCP / Azure / DigitalOcean `BucketProvisioner` implementations.
- "Manage" page native to Settings (replaces the deep-link to /nodes for bucket-level operations).
- Bucket-level metrics in the Settings list (last-write timestamp, object count) using the SDK or transport_configs counters.
- Soft-delete with retention window for transport_configs to allow recovery from accidental deletion.
- MinIO embedded provisioning (run MinIO as a Docker sidecar on the parent host).
- Dedicated "Rotate keys" wizard with explicit re-enrollment guidance (replaces the cut v1 PATCH-side regenerate flag).
