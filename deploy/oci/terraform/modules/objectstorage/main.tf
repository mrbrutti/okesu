# OCI Object Storage — S3-compat blob store.
#
# Two pieces: the bucket itself, and a "Customer Secret Key" attached
# to the user — that's what the S3 SDK uses to authenticate against
# the OCI compatibility endpoint.
#
# Cost: per-GB storage + per-request. Smoke volumes are pennies.

variable "compartment_ocid" { type = string }
variable "tenancy_ocid" { type = string }
variable "user_ocid" { type = string }
variable "name_prefix" { type = string }
variable "region" { type = string }
variable "secrets_dir" { type = string }

data "oci_objectstorage_namespace" "ns" {
  compartment_id = var.tenancy_ocid
}

resource "oci_objectstorage_bucket" "main" {
  compartment_id = var.compartment_ocid
  namespace      = data.oci_objectstorage_namespace.ns.namespace
  name           = "${var.name_prefix}-bucket"
  access_type    = "NoPublicAccess"
  storage_tier   = "Standard"
}

resource "oci_identity_customer_secret_key" "main" {
  user_id      = var.user_ocid
  display_name = "${var.name_prefix}-csk"
}

resource "local_sensitive_file" "blob_secret" {
  filename        = "${var.secrets_dir}/blob/secret-key"
  content         = oci_identity_customer_secret_key.main.key
  file_permission = "0600"
}

# ── Outputs ─────────────────────────────────────────────────────────
output "endpoint" {
  description = "S3-compat endpoint for the cp.yaml blob_url."
  value       = "${data.oci_objectstorage_namespace.ns.namespace}.compat.objectstorage.${var.region}.oraclecloud.com"
}

output "bucket_name" {
  value = oci_objectstorage_bucket.main.name
}

output "access_key" {
  description = "Customer Secret Key access ID — usable as an AWS_ACCESS_KEY_ID."
  value       = oci_identity_customer_secret_key.main.id
}

output "namespace" {
  value = data.oci_objectstorage_namespace.ns.namespace
}

# Always-generated federation token. Whether the CP USES it is a
# config-time decision in cp.yaml.tmpl (only mode=parent emits the
# federation_token line referencing it); generating it unconditionally
# keeps the terraform graph free of mode-conditional branching.
resource "random_password" "federation_token" {
  length  = 32
  special = false
}

resource "local_sensitive_file" "federation_token" {
  filename        = "${var.secrets_dir}/federation/token"
  content         = random_password.federation_token.result
  file_permission = "0600"
}

output "federation_token_path" {
  description = "Local file holding the federation token (for parent mode operators)."
  value       = local_sensitive_file.federation_token.filename
}

output "federation_outputs" {
  description = "Bundle parent-mode operators paste into a child's tfvars."
  value = {
    parent_federation_bucket     = oci_objectstorage_bucket.main.name
    parent_federation_endpoint   = "${data.oci_objectstorage_namespace.ns.namespace}.compat.objectstorage.${var.region}.oraclecloud.com"
    parent_federation_region     = var.region
    parent_federation_access_key = oci_identity_customer_secret_key.main.id
    parent_federation_token      = random_password.federation_token.result
  }
  sensitive = true
}
