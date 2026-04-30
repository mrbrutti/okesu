# OCI Cache (Redis) — pub/sub fan-out for the stateless CP.
#
# Smallest tier: 2 GB single-node, ~$0.05/hr. Authentication uses an
# auth token; we pin a generated one and write it to the secrets dir.

variable "compartment_ocid" { type = string }
variable "name_prefix" { type = string }
variable "subnet_ocid" { type = string }
variable "secrets_dir" { type = string }

resource "random_password" "auth" {
  length  = 32
  special = false
}

resource "local_sensitive_file" "redis_auth" {
  filename        = "${var.secrets_dir}/redis/auth-token"
  content         = random_password.auth.result
  file_permission = "0600"
}

resource "oci_redis_redis_cluster" "main" {
  compartment_id     = var.compartment_ocid
  display_name       = "${var.name_prefix}-redis"
  software_version   = "REDIS_7_0"
  node_count         = 1
  node_memory_in_gbs = 2
  subnet_id          = var.subnet_ocid
}

# OCI Cache exposes a private endpoint as the primary FQDN.
locals {
  endpoint = oci_redis_redis_cluster.main.primary_fqdn
}

# ── Outputs ─────────────────────────────────────────────────────────
output "url" {
  description = "redis:// URL for cp.yaml's pubsub_url."
  value       = "redis://:${random_password.auth.result}@${local.endpoint}:6379/0"
  sensitive   = true
}

output "url_no_password" {
  description = "Sanitized URL for non-sensitive logging."
  value       = "redis://${local.endpoint}:6379/0"
}

output "cluster_id" {
  value = oci_redis_redis_cluster.main.id
}

output "endpoint" {
  description = "Bare FQDN — Makefile/render package combine with port + auth themselves."
  value       = local.endpoint
}
