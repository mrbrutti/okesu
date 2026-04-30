variable "compartment_ocid" { type = string }
variable "name_prefix" { type = string }
variable "subnet_ocid" { type = string }
variable "shape" { type = string }
variable "image_ocid" { type = string }
variable "ssh_public_key" { type = string }
variable "ocpus" { type = number }
variable "memory_in_gbs" { type = number }
variable "clickhouse_version" { type = string }
variable "clickhouse_password" {
  type      = string
  sensitive = true
}

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
  cloudinit = templatefile("${path.module}/cloudinit.sh.tftpl", {
    clickhouse_version  = var.clickhouse_version
    clickhouse_password = var.clickhouse_password
  })
}

resource "oci_core_instance" "ch" {
  compartment_id      = var.compartment_ocid
  display_name        = "${var.name_prefix}-ch"
  shape               = var.shape
  availability_domain = data.oci_identity_availability_domains.ads.availability_domains[0].name

  source_details {
    source_type = "image"
    source_id   = local.resolved_image_ocid
  }

  shape_config {
    ocpus         = var.ocpus
    memory_in_gbs = var.memory_in_gbs
  }

  create_vnic_details {
    subnet_id        = var.subnet_ocid
    assign_public_ip = false
  }

  metadata = {
    ssh_authorized_keys = var.ssh_public_key
    user_data           = base64encode(local.cloudinit)
  }
}

output "private_ip" { value = oci_core_instance.ch.private_ip }
