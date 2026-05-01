provider "oci" {
  # Two auth modes supported via var.oci_auth:
  #
  #   "ApiKey"        — default. Reads user_ocid + fingerprint + key_file
  #                     from ~/.oci/config[<profile>]. Long-lived API key.
  #   "SecurityToken" — for `oci session authenticate` (browser SSO).
  #                     Reads the session from ~/.oci/sessions/<profile>/.
  #                     Token expires after ~1h; re-auth refreshes it.
  #
  # The provider also needs tenancy_ocid + region either from the config
  # file or via these explicit fields.
  auth                = var.oci_auth
  config_file_profile = var.oci_config_profile
  tenancy_ocid        = var.tenancy_ocid
  region              = var.region
}

# ── Network: foundational, every other module depends on it ─────────
module "network" {
  source = "./modules/network"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
}

# ── Managed services: only provisioned when smoke=false ─────────────
module "db" {
  count  = var.smoke ? 0 : 1
  source = "./modules/db"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
  subnet_ocid      = module.network.private_subnet_ocid
  vcn_ocid         = module.network.vcn_ocid
  admin_username   = var.db_admin_username
  secrets_dir      = pathexpand(var.secrets_dir)
}

module "streaming" {
  count  = var.smoke ? 0 : 1
  source = "./modules/streaming"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
  user_ocid        = var.user_ocid
  tenancy_ocid     = var.tenancy_ocid
  secrets_dir      = pathexpand(var.secrets_dir)
}

module "cache" {
  count  = var.smoke ? 0 : 1
  source = "./modules/cache"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
  subnet_ocid      = module.network.private_subnet_ocid
  secrets_dir      = pathexpand(var.secrets_dir)
}

module "objectstorage" {
  count  = var.smoke ? 0 : 1
  source = "./modules/objectstorage"

  compartment_ocid = var.compartment_ocid
  tenancy_ocid     = var.tenancy_ocid
  user_ocid        = var.user_ocid
  name_prefix      = var.name_prefix
  region           = var.region
  secrets_dir      = pathexpand(var.secrets_dir)
}

# ── CP VM ────────────────────────────────────────────────────────────
module "cp_vm" {
  source = "./modules/cp_vm"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
  subnet_ocid      = module.network.public_subnet_ocid
  shape            = var.cp_shape
  ocpus            = var.cp_ocpus
  memory_in_gbs    = var.cp_memory_in_gbs
  image_ocid       = var.cp_image_ocid
  ssh_public_key   = var.ssh_public_key
}

# ── ClickHouse VM — only provisioned when smoke=false ────────────────
module "clickhouse_vm" {
  count  = var.smoke ? 0 : 1
  source = "./modules/clickhouse_vm"

  compartment_ocid    = var.compartment_ocid
  name_prefix         = var.name_prefix
  subnet_ocid         = module.network.private_subnet_ocid
  shape               = var.clickhouse_shape
  ocpus               = var.clickhouse_ocpus
  memory_in_gbs       = var.clickhouse_memory_in_gbs
  image_ocid          = var.clickhouse_image_ocid
  ssh_public_key      = var.ssh_public_key
  clickhouse_version  = var.clickhouse_version
  clickhouse_password = random_password.clickhouse.result
}

# ── Fleet: daemon VMs — only provisioned when smoke=false ────────────
module "fleet" {
  count  = var.smoke ? 0 : 1
  source = "./modules/fleet"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
  subnet_ocid      = module.network.public_subnet_ocid
  fleet_size       = var.fleet_size
  shape            = var.fleet_shape
  image_ocid       = var.fleet_image_ocid
  ssh_public_key   = var.ssh_public_key
}
