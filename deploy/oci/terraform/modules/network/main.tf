# Network: VCN with three subnets.
#
#   public/   10.0.0.0/24    fleet VMs + LB; routes to internet gateway
#   private/  10.0.1.0/24    Postgres + Cache; routes via NAT only
#   oke/      10.0.2.0/24    OKE worker subnet; routes via NAT
#
# Free in OCI — VCN, subnets, gateways, route tables, security lists
# are all infrastructure resources without per-hour billing.

variable "compartment_ocid" { type = string }
variable "name_prefix" { type = string }

resource "oci_core_vcn" "main" {
  compartment_id = var.compartment_ocid
  cidr_blocks    = ["10.0.0.0/16"]
  display_name   = "${var.name_prefix}-vcn"
  dns_label      = "okesusmoke"
}

# ── Gateways ────────────────────────────────────────────────────────
resource "oci_core_internet_gateway" "igw" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${var.name_prefix}-igw"
}

resource "oci_core_nat_gateway" "ngw" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${var.name_prefix}-ngw"
}

resource "oci_core_service_gateway" "sgw" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${var.name_prefix}-sgw"

  services {
    service_id = data.oci_core_services.all_oci.services[0].id
  }
}

data "oci_core_services" "all_oci" {
  filter {
    name   = "name"
    values = ["All .* Services In Oracle Services Network"]
    regex  = true
  }
}

# ── Route tables ────────────────────────────────────────────────────
resource "oci_core_route_table" "public" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${var.name_prefix}-rt-public"

  route_rules {
    destination       = "0.0.0.0/0"
    destination_type  = "CIDR_BLOCK"
    network_entity_id = oci_core_internet_gateway.igw.id
  }
}

resource "oci_core_route_table" "private" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${var.name_prefix}-rt-private"

  route_rules {
    destination       = "0.0.0.0/0"
    destination_type  = "CIDR_BLOCK"
    network_entity_id = oci_core_nat_gateway.ngw.id
  }

  route_rules {
    destination       = data.oci_core_services.all_oci.services[0].cidr_block
    destination_type  = "SERVICE_CIDR_BLOCK"
    network_entity_id = oci_core_service_gateway.sgw.id
  }
}

# ── Security list — public subnet ───────────────────────────────────
# SSH + ICMP from anywhere (smoke; lock down for production).
# Internal traffic open within the VCN.
resource "oci_core_security_list" "public" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${var.name_prefix}-sl-public"

  egress_security_rules {
    destination = "0.0.0.0/0"
    protocol    = "all"
  }

  ingress_security_rules {
    protocol = "6" # TCP
    source   = "0.0.0.0/0"
    tcp_options {
      min = 22
      max = 22
    }
  }

  # CP UI/webhook port
  ingress_security_rules {
    protocol = "6" # TCP
    source   = "0.0.0.0/0"
    tcp_options {
      min = 8443
      max = 8443
    }
  }

  # CP mgmt mTLS port
  ingress_security_rules {
    protocol = "6" # TCP
    source   = "0.0.0.0/0"
    tcp_options {
      min = 8444
      max = 8444
    }
  }

  ingress_security_rules {
    protocol = "1" # ICMP
    source   = "0.0.0.0/0"
  }

  # Intra-VCN — daemons reach Cache/DB through this.
  ingress_security_rules {
    protocol = "all"
    source   = "10.0.0.0/16"
  }
}

# ── Security list — private subnet ──────────────────────────────────
# Closed to public ingress; only intra-VCN ports for the managed services.
resource "oci_core_security_list" "private" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${var.name_prefix}-sl-private"

  egress_security_rules {
    destination = "0.0.0.0/0"
    protocol    = "all"
  }

  ingress_security_rules {
    protocol = "6"
    source   = "10.0.0.0/16"
    tcp_options {
      min = 5432 # Postgres
      max = 5432
    }
  }

  ingress_security_rules {
    protocol = "6"
    source   = "10.0.0.0/16"
    tcp_options {
      min = 6379 # Redis
      max = 6379
    }
  }

  # ClickHouse — explicit rule (functionally redundant given the
  # all-protocols intra-VCN rule on the public SL, but documents intent
  # and lets us tighten 5432/6379-only later if we ever drop the wildcard).
  ingress_security_rules {
    protocol = "6"
    source   = "10.0.0.0/16"
    tcp_options {
      min = 9000
      max = 9000
    }
  }
}

# ── Subnets ─────────────────────────────────────────────────────────
resource "oci_core_subnet" "public" {
  compartment_id    = var.compartment_ocid
  vcn_id            = oci_core_vcn.main.id
  cidr_block        = "10.0.0.0/24"
  display_name      = "${var.name_prefix}-public"
  dns_label         = "public"
  route_table_id    = oci_core_route_table.public.id
  security_list_ids = [oci_core_security_list.public.id]

  prohibit_public_ip_on_vnic = false
}

resource "oci_core_subnet" "private" {
  compartment_id    = var.compartment_ocid
  vcn_id            = oci_core_vcn.main.id
  cidr_block        = "10.0.1.0/24"
  display_name      = "${var.name_prefix}-private"
  dns_label         = "private"
  route_table_id    = oci_core_route_table.private.id
  security_list_ids = [oci_core_security_list.private.id]

  prohibit_public_ip_on_vnic = true
}

resource "oci_core_subnet" "oke" {
  compartment_id    = var.compartment_ocid
  vcn_id            = oci_core_vcn.main.id
  cidr_block        = "10.0.2.0/24"
  display_name      = "${var.name_prefix}-oke"
  dns_label         = "oke"
  route_table_id    = oci_core_route_table.private.id
  security_list_ids = [oci_core_security_list.private.id]

  prohibit_public_ip_on_vnic = true
}

# ── Outputs ─────────────────────────────────────────────────────────
output "vcn_ocid" { value = oci_core_vcn.main.id }
output "public_subnet_ocid" { value = oci_core_subnet.public.id }
output "private_subnet_ocid" { value = oci_core_subnet.private.id }
output "oke_subnet_ocid" { value = oci_core_subnet.oke.id }
