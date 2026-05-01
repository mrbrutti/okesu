# Outputs are consumed by the smoke runbook to populate cp.yaml.
# Secrets (passwords, secret keys) are NOT exposed here — they go to
# the secrets_dir as files. The CP reads them via --secrets-source.
#
# Conditional modules (db, streaming, cache, objectstorage,
# clickhouse_vm, fleet) are gated by var.smoke. Their outputs use
# try(module.X[0].attr, null) so smoke runs return null without error.

# ── DB ──────────────────────────────────────────────────────────────
output "db_dsn" {
  description = "Postgres DSN for cp.yaml (no password — pulled from secrets dir). Null in smoke mode."
  value       = try(module.db[0].dsn, null)
}

output "db_dsn_with_password" {
  description = "Full DSN including password — for psql / pg_dump only. Sensitive. Null in smoke mode."
  value       = try(module.db[0].dsn_with_password, null)
  sensitive   = true
}

# ── Streaming ───────────────────────────────────────────────────────
output "kafka_brokers" {
  description = "OCI Streaming bootstrap broker (Kafka API). Null in smoke mode."
  value       = try(module.streaming[0].broker_endpoint, null)
}

output "kafka_username" {
  description = "OCI-formatted SASL username: <tenancy>/<user>/<stream-pool-ocid>. Null in smoke mode."
  value       = try(module.streaming[0].sasl_username, null)
}

output "stream_pool_id" {
  description = "Stream pool OCID — for `oci streaming admin stream-pool get`. Null in smoke mode."
  value       = try(module.streaming[0].stream_pool_id, null)
}

# ── Cache ───────────────────────────────────────────────────────────
output "redis_url" {
  description = "redis:// URL for the CP's --pubsub-url. Null in smoke mode."
  value       = try(module.cache[0].url, null)
  sensitive   = true
}

output "redis_host" {
  description = "Redis FQDN (no scheme, no auth). Null in smoke mode."
  value       = try(module.cache[0].endpoint, null)
}

output "redis_port" {
  description = "Redis port."
  value       = 6379
}

# ── Object Storage ──────────────────────────────────────────────────
output "blob_endpoint" {
  description = "<namespace>.compat.objectstorage.<region>.oraclecloud.com. Null in smoke mode."
  value       = try(module.objectstorage[0].endpoint, null)
}

output "blob_bucket" {
  description = "Bucket name. Null in smoke mode."
  value       = try(module.objectstorage[0].bucket_name, null)
}

output "blob_access_key" {
  description = "Customer Secret Key access ID (the user-facing AKID). Null in smoke mode."
  value       = try(module.objectstorage[0].access_key, null)
}

# ── Fleet ───────────────────────────────────────────────────────────
output "fleet_ips" {
  description = "Public IPs of the daemon VMs — feed into the CP's Add Node flow. Empty in smoke mode."
  value       = try(module.fleet[0].public_ips, [])
}

output "fleet_size_actual" {
  description = "Number of VMs actually provisioned. 0 in smoke mode."
  value       = try(module.fleet[0].size, 0)
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
  description = "Private IP — fed into cp.yaml's clickhouse_addrs. Null in smoke mode."
  value       = try(module.clickhouse_vm[0].private_ip, null)
}

# ── ClickHouse password (sensitive) ──────────────────────────────────
output "clickhouse_password_path" {
  description = "Local path where the password file lives."
  value       = local_sensitive_file.clickhouse_password.filename
}

# ── Federation outputs (parent mode operators paste these into a child's tfvars) ───
output "federation_outputs" {
  description = "Parent-mode bundle for child enrollment. Null in smoke mode."
  value       = try(module.objectstorage[0].federation_outputs, null)
  sensitive   = true
}

# ── Region (for blob_region in cp.yaml) ──────────────────────────────
output "blob_region" {
  description = "Blob region (mirrors var.region)."
  value       = var.region
}
