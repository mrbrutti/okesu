# OCI Object Storage — S3-compat blob store.
#
# Two pieces: the bucket itself, and a "Customer Secret Key" attached
# to the user — that's what the S3 SDK uses to authenticate against
# the OCI compatibility endpoint.
#
# Cost: per-GB storage + per-request. Smoke volumes are pennies.

variable "compartment_ocid" { type = string }
variable "tenancy_ocid"     { type = string }
variable "user_ocid"        { type = string }
variable "name_prefix"      { type = string }
variable "region"           { type = string }
variable "secrets_dir"      { type = string }

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
