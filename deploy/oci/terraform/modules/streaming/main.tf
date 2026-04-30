# OCI Streaming — Kafka-API queue.
#
# A "stream pool" is the namespace; "streams" within it are topics. We
# pre-create the topics the CP needs so the worker doesn't have to do
# it lazily.
#
# Cost: per-message + per-storage; effectively free at smoke volumes.
#
# SASL: OCI Streaming uses PLAIN auth with a triple-OCID username and
# an auth token (NOT the API key) as the password. This module
# provisions the auth token via the IAM auth-token resource and emits
# the formatted username string.

variable "compartment_ocid" { type = string }
variable "name_prefix" { type = string }
variable "user_ocid" { type = string }
variable "tenancy_ocid" { type = string }
variable "secrets_dir" { type = string }

resource "oci_streaming_stream_pool" "main" {
  compartment_id = var.compartment_ocid
  name           = "${var.name_prefix}-pool"
}

resource "oci_streaming_stream" "events_raw" {
  compartment_id     = var.compartment_ocid
  stream_pool_id     = oci_streaming_stream_pool.main.id
  name               = "events.raw"
  partitions         = 4
  retention_in_hours = 24
}

resource "oci_identity_auth_token" "kafka" {
  user_id     = var.user_ocid
  description = "${var.name_prefix} Kafka SASL password"
}

resource "local_sensitive_file" "kafka_password" {
  filename        = "${var.secrets_dir}/kafka/sasl-password"
  content         = oci_identity_auth_token.kafka.token
  file_permission = "0600"
}

# ── Outputs ─────────────────────────────────────────────────────────
output "broker_endpoint" {
  description = "Bootstrap broker, ready for kafka_brokers[] in cp.yaml."
  value       = oci_streaming_stream_pool.main.endpoint_fqdn != null ? "${oci_streaming_stream_pool.main.endpoint_fqdn}:9092" : oci_streaming_stream_pool.main.kafka_settings[0].bootstrap_servers
}

output "sasl_username" {
  description = "Triple-OCID username for SASL/PLAIN."
  value       = "${var.tenancy_ocid}/${var.user_ocid}/${oci_streaming_stream_pool.main.id}"
}

output "stream_pool_id" {
  value = oci_streaming_stream_pool.main.id
}
