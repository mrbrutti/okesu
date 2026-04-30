variable "compartment_ocid" { type = string }
variable "name_prefix" { type = string }
variable "subnet_ocid" { type = string }
variable "shape" { type = string }
variable "image_ocid" { type = string }
variable "ssh_public_key" { type = string }

data "oci_identity_availability_domains" "ads" {
  compartment_id = var.compartment_ocid
}

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
  cloudinit           = templatefile("${path.module}/cloudinit.sh.tftpl", {})
}

resource "oci_core_instance" "cp" {
  compartment_id      = var.compartment_ocid
  display_name        = "${var.name_prefix}-cp"
  shape               = var.shape
  availability_domain = data.oci_identity_availability_domains.ads.availability_domains[0].name

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
    user_data           = base64encode(local.cloudinit)
  }
}

output "public_ip" { value = oci_core_instance.cp.public_ip }
output "private_ip" { value = oci_core_instance.cp.private_ip }
output "instance_id" { value = oci_core_instance.cp.id }
