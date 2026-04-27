provider "oci" {
  # Reads tenancy_ocid, user_ocid, fingerprint, key_file from
  # ~/.oci/config[DEFAULT] automatically. Override the profile via
  # the OCI_CONFIG_PROFILE environment variable if you have several.
  region = var.region
}

# ── Network: foundational, every other module depends on it ─────────
module "network" {
  source = "./modules/network"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
}

# ── Managed services: validates Postgres + Streaming + Cache + Object Storage ─
module "db" {
  source = "./modules/db"

  compartment_ocid    = var.compartment_ocid
  name_prefix         = var.name_prefix
  subnet_ocid         = module.network.private_subnet_ocid
  vcn_ocid            = module.network.vcn_ocid
  admin_username      = var.db_admin_username
  secrets_dir         = pathexpand(var.secrets_dir)
}

module "streaming" {
  source = "./modules/streaming"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
  user_ocid        = var.user_ocid
  tenancy_ocid     = var.tenancy_ocid
  secrets_dir      = pathexpand(var.secrets_dir)
}

module "cache" {
  source = "./modules/cache"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
  subnet_ocid      = module.network.private_subnet_ocid
  secrets_dir      = pathexpand(var.secrets_dir)
}

module "objectstorage" {
  source = "./modules/objectstorage"

  compartment_ocid = var.compartment_ocid
  tenancy_ocid     = var.tenancy_ocid
  user_ocid        = var.user_ocid
  name_prefix      = var.name_prefix
  region           = var.region
  secrets_dir      = pathexpand(var.secrets_dir)
}

# ── OKE + ClickHouse: validates the events firehose ─────────────────
module "oke" {
  source = "./modules/oke"

  compartment_ocid    = var.compartment_ocid
  name_prefix         = var.name_prefix
  vcn_ocid            = module.network.vcn_ocid
  oke_subnet_ocid     = module.network.oke_subnet_ocid
  public_subnet_ocid  = module.network.public_subnet_ocid
  region              = var.region
  secrets_dir         = pathexpand(var.secrets_dir)
}

# ── Fleet: 8 VMs running the daemon ─────────────────────────────────
module "fleet" {
  source = "./modules/fleet"

  compartment_ocid = var.compartment_ocid
  name_prefix      = var.name_prefix
  subnet_ocid      = module.network.public_subnet_ocid
  fleet_size       = var.fleet_size
  shape            = var.fleet_shape
  image_ocid       = var.fleet_image_ocid
  ssh_public_key   = var.ssh_public_key
}
