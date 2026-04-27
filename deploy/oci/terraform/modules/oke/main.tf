# OKE cluster + ClickHouse via the bitnami Helm chart.
#
# OKE control plane is FREE in OCI; only worker nodes cost compute.
# We start with 1 worker node on the smallest E2 shape — adequate for
# a single-replica ClickHouse smoke test.
#
# Cost: ~$0.005/hr (1 × E2.1.Micro).

variable "compartment_ocid"   { type = string }
variable "name_prefix"        { type = string }
variable "vcn_ocid"           { type = string }
variable "oke_subnet_ocid"    { type = string }
variable "public_subnet_ocid" { type = string }
variable "region"             { type = string }
variable "secrets_dir"        { type = string }

# ── OKE cluster ─────────────────────────────────────────────────────
resource "oci_containerengine_cluster" "main" {
  compartment_id     = var.compartment_ocid
  kubernetes_version = "v1.30.1"
  name               = "${var.name_prefix}-oke"
  vcn_id             = var.vcn_ocid

  endpoint_config {
    is_public_ip_enabled = true
    subnet_id            = var.public_subnet_ocid
  }

  options {
    service_lb_subnet_ids = [var.public_subnet_ocid]
  }
}

data "oci_identity_availability_domains" "ads" {
  compartment_id = var.compartment_ocid
}

resource "oci_containerengine_node_pool" "main" {
  compartment_id     = var.compartment_ocid
  cluster_id         = oci_containerengine_cluster.main.id
  kubernetes_version = "v1.30.1"
  name               = "${var.name_prefix}-pool"
  node_shape         = "VM.Standard.E2.1.Micro"

  node_config_details {
    placement_configs {
      availability_domain = data.oci_identity_availability_domains.ads.availability_domains[0].name
      subnet_id           = var.oke_subnet_ocid
    }
    size = 1
  }

  node_source_details {
    source_type = "IMAGE"
    image_id    = data.oci_core_images.oke_node.images[0].id
  }
}

# OKE-published worker node images.
data "oci_core_images" "oke_node" {
  compartment_id           = var.compartment_ocid
  operating_system         = "Oracle Linux"
  operating_system_version = "8"
  shape                    = "VM.Standard.E2.1.Micro"
  state                    = "AVAILABLE"

  filter {
    name   = "display_name"
    values = ["^.*OKE.*$"]
    regex  = true
  }
}

# ── Kubeconfig ──────────────────────────────────────────────────────
data "oci_containerengine_cluster_kube_config" "main" {
  cluster_id = oci_containerengine_cluster.main.id
}

resource "local_sensitive_file" "kubeconfig" {
  filename        = "${var.secrets_dir}/oke/kubeconfig"
  content         = data.oci_containerengine_cluster_kube_config.main.content
  file_permission = "0600"
}

# ── ClickHouse via Helm ─────────────────────────────────────────────
provider "helm" {
  alias = "oke"
  # helm provider 3.x — kubernetes is an attribute, not a block.
  kubernetes = {
    config_path = local_sensitive_file.kubeconfig.filename
  }
}

resource "random_password" "clickhouse" {
  length  = 32
  special = false
}

resource "local_sensitive_file" "clickhouse_password" {
  filename        = "${var.secrets_dir}/clickhouse/password"
  content         = random_password.clickhouse.result
  file_permission = "0600"
}

resource "helm_release" "clickhouse" {
  provider   = helm.oke
  name       = "clickhouse"
  repository = "https://charts.bitnami.com/bitnami"
  chart      = "clickhouse"
  namespace  = "okesu"

  create_namespace = true
  wait             = true
  timeout          = 600

  # helm provider 3.x — set / set_sensitive are list attributes, not blocks.
  set = [
    { name = "auth.username", value = "default" },
    { name = "shards",        value = "1" },
    { name = "replicaCount",  value = "1" },
    { name = "zookeeper.enabled", value = "false" },
  ]
  set_sensitive = [
    { name = "auth.password", value = random_password.clickhouse.result },
  ]
}

# ── Outputs ─────────────────────────────────────────────────────────
output "cluster_id" {
  value = oci_containerengine_cluster.main.id
}

output "kubeconfig_path" {
  value = local_sensitive_file.kubeconfig.filename
}

output "clickhouse_service" {
  description = "Cluster-internal address of ClickHouse."
  value       = "clickhouse.okesu.svc.cluster.local:9000"
}
