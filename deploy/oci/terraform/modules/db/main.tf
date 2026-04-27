# OCI Database with PostgreSQL — managed Postgres.
#
# Smallest tier: 2 OCPU + 32 GB RAM, ~$0.10/hr.
# Postgres major version 16 (matches dev/docker-compose).

variable "compartment_ocid" { type = string }
variable "name_prefix"      { type = string }
variable "subnet_ocid"      { type = string }
variable "vcn_ocid"         { type = string }
variable "admin_username"   { type = string }
variable "secrets_dir"      { type = string }

# Generate a random admin password and write it to the secrets dir
# where the CP's file:// secrets adapter can read it.
resource "random_password" "admin" {
  length      = 32
  special     = true
  min_special = 4
  override_special = "!#$%&*()-_=+"
}

resource "local_sensitive_file" "admin_password" {
  filename        = "${var.secrets_dir}/cp/db-admin-password"
  content         = random_password.admin.result
  file_permission = "0600"
}

resource "oci_psql_db_system" "main" {
  compartment_id   = var.compartment_ocid
  display_name     = "${var.name_prefix}-pg"
  db_version       = "16"
  shape            = "PostgreSQL.VM.Standard.E4.Flex.2.32GB"
  storage_details {
    is_regionally_durable = true
    system_type           = "OCI_OPTIMIZED_STORAGE"
  }
  network_details {
    subnet_id = var.subnet_ocid
  }
  credentials {
    username = var.admin_username
    password_details {
      password      = random_password.admin.result
      password_type = "PLAIN_TEXT"
    }
  }
  instance_count = 1
}

# OCI returns multiple endpoints; pick the primary.
locals {
  primary_endpoint = oci_psql_db_system.main.instances[0].private_ip
  database_name    = "cpdb"
}

# ── Outputs ─────────────────────────────────────────────────────────
output "dsn" {
  value = "postgres://${var.admin_username}@${local.primary_endpoint}:5432/${local.database_name}?sslmode=require"
}

output "dsn_with_password" {
  value     = "postgres://${var.admin_username}:${random_password.admin.result}@${local.primary_endpoint}:5432/${local.database_name}?sslmode=require"
  sensitive = true
}

output "endpoint" {
  value = local.primary_endpoint
}

output "system_id" {
  value = oci_psql_db_system.main.id
}
