variable "oci_auth" {
  description = "OCI provider auth mode. \"ApiKey\" for long-lived API keys, \"SecurityToken\" for `oci session authenticate` (browser SSO)."
  type        = string
  default     = "SecurityToken"
  validation {
    condition     = contains(["ApiKey", "SecurityToken", "InstancePrincipal"], var.oci_auth)
    error_message = "oci_auth must be ApiKey, SecurityToken, or InstancePrincipal."
  }
}

variable "oci_config_profile" {
  description = "Profile name in ~/.oci/config (or ~/.oci/sessions/<name>/ for SecurityToken). Default: DEFAULT."
  type        = string
  default     = "DEFAULT"
}

variable "tenancy_ocid" {
  description = "OCID of the tenancy. Find with: oci iam compartment list --compartment-id-in-subtree true and look at parent_compartment_id."
  type        = string
}

variable "user_ocid" {
  description = "OCID of the API-key user. Pulled from ~/.oci/config."
  type        = string
}

variable "compartment_ocid" {
  description = "OCID of the compartment that holds every resource the smoke creates. Recommend a dedicated compartment so teardown is unambiguous."
  type        = string
}

variable "region" {
  description = "OCI region. Defaults to us-ashburn-1 to match the cp.example.yaml placeholders."
  type        = string
  default     = "us-ashburn-1"
}

variable "name_prefix" {
  description = "Prefix for every resource's display name. Lets multiple smoke runs coexist if you forget to tear one down."
  type        = string
  default     = "okesu-smoke"
}

variable "fleet_size" {
  description = "Daemon VM count. The first 2 are Always Free (E2.1.Micro); each additional is ~$0.005/hr."
  type        = number
  default     = 8
}

variable "ssh_public_key" {
  description = "SSH public key (full contents, one line) to install on every fleet VM. The matching private key never leaves your laptop."
  type        = string
}

variable "fleet_image_ocid" {
  description = "Image OCID for fleet VMs. Default is Oracle Linux 9 amd64 in us-ashburn-1 — override for other regions. List with: oci compute image list --compartment-id <tenancy_ocid> --operating-system 'Oracle Linux' --shape VM.Standard.E2.1.Micro."
  type        = string
  default     = ""
}

variable "fleet_shape" {
  description = "Compute shape for fleet VMs. E2.1.Micro is Always Free (2/account); E2.2.Micro is the next step up."
  type        = string
  default     = "VM.Standard.E2.1.Micro"
}

variable "db_admin_username" {
  description = "Admin user for the managed Postgres."
  type        = string
  default     = "okesu"
}

variable "secrets_dir" {
  description = "Local directory where Terraform writes generated passwords (mode 0600). Read by the CP's --secrets-source file://… adapter."
  type        = string
  default     = "~/.okesu-secrets"
}
