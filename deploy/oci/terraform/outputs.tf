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

# ── Fleet ───────────────────────────────────────────────────────────
output "fleet_ips" {
  description = "Public IPs of the daemon VMs — feed into the CP's Add Node flow."
  value       = module.fleet.public_ips
}

output "fleet_size_actual" {
  description = "Number of VMs actually provisioned."
  value       = module.fleet.size
}

# ── CP VM ────────────────────────────────────────────────────────────
output "cp_public_ip" {
  description = "Public IP for SSH + the CP UI (https://<ip>:8443)."
  value       = module.cp_vm.public_ip
}

output "cp_private_ip" {
  description = "Private IP for intra-VCN access."
  value       = module.cp_vm.private_ip
}

# ── ClickHouse VM ────────────────────────────────────────────────────
output "ch_private_ip" {
  description = "Private IP — fed into cp.yaml's clickhouse_addrs."
  value       = module.clickhouse_vm.private_ip
}

# ── ClickHouse password (sensitive) ──────────────────────────────────
output "clickhouse_password_path" {
  description = "Local path where the password file lives."
  value       = local_sensitive_file.clickhouse_password.filename
}

# ── Federation outputs (parent mode operators paste these into a child's tfvars) ───
output "federation_outputs" {
  description = "Parent-mode bundle for child enrollment."
  value       = module.objectstorage.federation_outputs
  sensitive   = true
}

# ── Cache (Redis) ────────────────────────────────────────────────────
output "redis_host" {
  description = "Redis FQDN (no scheme, no auth)."
  value       = module.cache.endpoint
}

output "redis_port" {
  description = "Redis port."
  value       = 6379
}

# ── Region (for blob_region in cp.yaml) ──────────────────────────────
output "blob_region" {
  description = "Blob region (mirrors var.region)."
  value       = var.region
}
