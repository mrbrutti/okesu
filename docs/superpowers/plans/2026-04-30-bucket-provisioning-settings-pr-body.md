## Summary

Native Settings UI for adding, editing, and deleting object-storage
buckets. Replaces the broken "Add bucket" link in
`Settings → Cloud → Object storage buckets` (which navigated to
`/nodes`) with a dedicated wizard that uses configured cloud
credentials + the cloud SDK to discover existing or create new
buckets — same UX pattern as Add-CP.

- New `BucketProvisioner` interface in `controlplane/cpprovision/`
  with implementations for AWS (S3 SDK), OCI (ObjectStorage +
  Customer Secret Keys via IAM), and MinIO (S3 SDK with custom
  endpoint).
- `"minio"` added to `db.AllowedCloudKinds`; Add Provider form gets
  a MinIO option.
- New endpoints: `GET /api/buckets/cloud-providers`,
  `GET /api/buckets/discover`, `POST /api/buckets/provision`.
- New `PATCH /api/transport-configs/{id}` with safe partial-update
  semantics (name + scanner_interval_ms only). Identity fields stay
  immutable.
- Existing `DELETE /api/transport-configs/{id}` tightened with an
  in-use guard returning 409 + a referencing-resources list when
  nodes or enrollment_packages reference the row.
- Frontend: `AddBucketWizard`, `EditBucketModal`, `DeleteBucketDialog`
  components. Settings → Cloud bucket section wires them up; Add
  Provider form gains a MinIO branch.

Build matrix unchanged: `CGO_ENABLED=0` everywhere.

## Test plan

Lab smoke (post-merge):

- [ ] `go test ./...` passes
- [ ] `npm run build` clean in `web/`
- [ ] Configure an AWS credential. Open Settings → Cloud → Add bucket.
      Pick AWS → Discover existing → pick a bucket → success: row
      appears in the list with the chosen bucket.
- [ ] Configure an OCI credential. Add bucket → OCI → Create new →
      enter name → success: bucket created in OCI tenancy with
      Customer Secret Keys auto-generated.
- [ ] Add a MinIO provider (Add Provider form, new MinIO branch).
      Add bucket → MinIO → Discover.
- [ ] Edit a bucket: change display name → save. Verify list reflects.
- [ ] Delete a bucket referenced by a node: 409 dialog shows the node
      with a deep-link.
- [ ] Delete an unreferenced bucket: confirms and removes.

## Files

- New backend: `controlplane/cpprovision/bucket_provisioner.go`,
  `controlplane/cpprovision/aws/bucket.go`,
  `controlplane/cpprovision/oci/bucket.go`,
  `controlplane/cpprovision/minio/bucket.go`,
  `controlplane/api/buckets.go`.
- New endpoints: `GET /api/buckets/cloud-providers`,
  `GET /api/buckets/discover`,
  `POST /api/buckets/provision`,
  `PATCH /api/transport-configs/{id}` (DELETE existing endpoint
  tightened in place).
- New frontend: `web/src/components/AddBucketWizard.tsx`,
  `web/src/components/EditBucketModal.tsx`,
  `web/src/components/DeleteBucketDialog.tsx`.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-04-30-bucket-provisioning-settings-design.md`
- Plan: `docs/superpowers/plans/2026-04-30-bucket-provisioning-settings.md`

## Open follow-ups

- GCP / Azure / DigitalOcean BucketProvisioner implementations
- Dedicated "Manage bucket" page in Settings (replaces deep-link to /nodes)
- Bucket-level metrics in the Settings list (last-write timestamp, object count)
- "Rotate keys" wizard with explicit re-enrollment guidance
- MinIO embedded provisioning (run MinIO as a Docker sidecar on the parent host)
