# Outputs are consumed by the smoke runbook to populate cp.yaml.
# Secrets (passwords, secret keys) are NOT exposed here — they go to
# the secrets_dir as files. The CP reads them via --secrets-source.

# ── DB ──────────────────────────────────────────────────────────────
output "db_dsn" {
  description = "Postgres DSN for cp.yaml (no password — pulled from secrets dir)."
  value       = module.db.dsn
}

output "db_dsn_with_password" {
  description = "Full DSN including password — for psql / pg_dump only. Sensitive."
  value       = module.db.dsn_with_password
  sensitive   = true
}

# ── Streaming ───────────────────────────────────────────────────────
output "kafka_brokers" {
  description = "OCI Streaming bootstrap broker (Kafka API)."
  value       = module.streaming.broker_endpoint
}

output "kafka_username" {
  description = "OCI-formatted SASL username: <tenancy>/<user>/<stream-pool-ocid>."
  value       = module.streaming.sasl_username
}

output "stream_pool_id" {
  description = "Stream pool OCID — for `oci streaming admin stream-pool get`."
  value       = module.streaming.stream_pool_id
}

# ── Cache ───────────────────────────────────────────────────────────
output "redis_url" {
  description = "redis:// URL for the CP's --pubsub-url."
  value       = module.cache.url
}

# ── Object Storage ──────────────────────────────────────────────────
output "blob_endpoint" {
  description = "<namespace>.compat.objectstorage.<region>.oraclecloud.com"
  value       = module.objectstorage.endpoint
}

output "blob_bucket" {
  description = "Bucket name."
  value       = module.objectstorage.bucket_name
}

output "blob_access_key" {
  description = "Customer Secret Key access ID (the user-facing AKID)."
  value       = module.objectstorage.access_key
}

# ── OKE ─────────────────────────────────────────────────────────────
output "oke_kubeconfig_path" {
  description = "Path to the kubeconfig Terraform fetched. Set KUBECONFIG to this."
  value       = module.oke.kubeconfig_path
}

output "oke_cluster_id" {
  description = "OKE cluster OCID."
  value       = module.oke.cluster_id
}

# ── Fleet ───────────────────────────────────────────────────────────
output "fleet_ips" {
  description = "Public IPs of the daemon VMs — feed into the CP's Add Node flow."
  value       = module.fleet.public_ips
}

output "fleet_size_actual" {
  description = "Number of VMs actually provisioned."
  value       = module.fleet.size
}
