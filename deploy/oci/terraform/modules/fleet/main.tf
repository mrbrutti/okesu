# Fleet of daemon VMs.
#
# 8 by default: the first 2 fit OCI's Always Free tier (E2.1.Micro × 2
# per account); the rest cost ~$0.005/hr each. The CP's SSH-deploy
# flow installs the okesu binary + edr.md agent file post-provision.

variable "compartment_ocid" { type = string }
variable "name_prefix"      { type = string }
variable "subnet_ocid"      { type = string }
variable "fleet_size"       { type = number }
variable "shape"            { type = string }
variable "image_ocid"       { type = string }
variable "ssh_public_key"   { type = string }

data "oci_identity_availability_domains" "ads" {
  compartment_id = var.compartment_ocid
}

# When the operator didn't supply an image OCID, look up the latest
# Oracle Linux 9 amd64 image. Region-specific — varies by tenancy.
data "oci_core_images" "ol9" {
  count                    = var.image_ocid == "" ? 1 : 0
  compartment_id           = var.compartment_ocid
  operating_system         = "Oracle Linux"
  operating_system_version = "9"
  shape                    = var.shape
  state                    = "AVAILABLE"

  filter {
    name   = "display_name"
    values = ["^Oracle-Linux-9.*"]
    regex  = true
  }
}

locals {
  resolved_image_ocid = var.image_ocid != "" ? var.image_ocid : data.oci_core_images.ol9[0].images[0].id
  ad_count            = length(data.oci_identity_availability_domains.ads.availability_domains)
}

resource "oci_core_instance" "fleet" {
  count = var.fleet_size

  compartment_id      = var.compartment_ocid
  display_name        = "${var.name_prefix}-fleet-${count.index}"
  shape               = var.shape
  availability_domain = data.oci_identity_availability_domains.ads.availability_domains[count.index % local.ad_count].name

  source_details {
    source_type = "image"
    source_id   = local.resolved_image_ocid
  }

  create_vnic_details {
    subnet_id        = var.subnet_ocid
    assign_public_ip = true
  }

  metadata = {
    ssh_authorized_keys = var.ssh_public_key
  }
}

# ── Outputs ─────────────────────────────────────────────────────────
output "public_ips" {
  description = "Ordered list of public IPs the CP can reach by SSH."
  value       = [for i in oci_core_instance.fleet : i.public_ip]
}

output "size" {
  value = length(oci_core_instance.fleet)
}

output "instance_ids" {
  value = [for i in oci_core_instance.fleet : i.id]
}
