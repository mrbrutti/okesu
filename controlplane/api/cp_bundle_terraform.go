// Terraform module export for child CP bootstrap.
//
// The Terraform path is the IaC alternative to "Managed deploy": the
// operator runs `terraform apply` from their workstation against
// their own cloud credentials, instead of letting the parent CP hold
// the credential and call the cloud APIs itself. The module wraps:
//
//   1. A provider-specific instance resource (oci_core_instance,
//      aws_instance, ...) with the operator's required knobs left as
//      Terraform variables.
//   2. A pre-rendered cloud-init.sh that — at instance boot — curls
//      the parent's bundle download endpoint with a one-time bearer
//      token, untars it, and `docker compose up -d`. This is the
//      same script the managed-deploy worker injects via user_data;
//      we just persist it to disk inside the module so terraform
//      filebase64() can include it in the resource.
//   3. A README explaining the variable contract + the security
//      story (token expires in 24h, do not commit *.tfvars).
//
// The actual Docker bundle bytes (dockerfile-tarball or
// compose-tarball — preferred order) are stashed in the parent's
// in-memory BundleCache keyed by the bootstrap token id. When the
// terraform-launched VM boots, its cloud-init fetches the bundle
// from /api/federation/cp-bundle/download with the bootstrap token
// as Bearer auth — exactly like managed-deploy does.
//
// Two-cloud scope for v1: oci + aws. Adding a new cloud means
// dropping a new template block + adding the cloud to
// isSupportedTerraformCloud().

package api

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"strings"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

// writeTerraformBundle renders a self-contained Terraform module as
// tar.gz to w. It also generates the underlying Docker bundle (compose
// preferred, dockerfile fallback) and stashes it in the cache so the
// rendered cloud-init script can fetch it once Terraform brings the
// VM up. The bootstrap token plaintext is baked into both the cloud-init
// and the README's variable defaults — this is the operator's only
// chance to copy it down.
func writeTerraformBundle(
	w io.Writer,
	b bundleVars,
	cloud string,
	cfg CPBundleConfig,
	cache *BundleCache,
	parentBaseURL string,
	tokenID int64,
) error {
	// 1. Build the underlying Docker bundle into memory + cache it
	//    keyed by the token id. Compose first because it gives the
	//    fastest VM boot; fall back to dockerfile if no image tar is
	//    configured. (The handler validated that at least one is
	//    available before reaching us.)
	bundleBytes, bundleSubFormat, err := renderInnerDockerBundle(b, cfg)
	if err != nil {
		return fmt.Errorf("render inner bundle: %w", err)
	}
	bundleFilename := fmt.Sprintf("okesu-cp-%s.tar.gz", slugify(b.DisplayName))
	cache.Put(tokenID, bundleFilename, bundleBytes)

	// 2. Render cloud-init.sh — same shape the managed-deploy worker
	//    uses, with bundle URL + token + filename baked in.
	bundleURL := strings.TrimRight(parentBaseURL, "/") + "/api/federation/cp-bundle/download"
	cloudInit, err := cpprovision.RenderCloudInit(cpprovision.CloudInitVars{
		DisplayName:    b.DisplayName,
		Region:         b.Region,
		BundleURL:      bundleURL,
		BundleToken:    b.BootstrapToken,
		BundleFilename: bundleFilename,
		ProvisionID:    tokenID, // fine for log greppability — token id is unique per bundle
	})
	if err != nil {
		return fmt.Errorf("render cloud-init: %w", err)
	}

	// 3. Pack the Terraform module + cloud-init + README into tar.gz.
	gz := gzip.NewWriter(w)
	defer gz.Close()
	t := tar.NewWriter(gz)
	defer t.Close()

	root := bundleRootDir(b)
	files := []bundleFile{
		{name: "versions.tf", mode: 0o644, content: tfVersionsTemplate(cloud)},
		{name: "variables.tf", mode: 0o644, content: tfVariablesTemplate(cloud, b)},
		{name: "main.tf", mode: 0o644, content: tfMainTemplate(cloud, b)},
		{name: "outputs.tf", mode: 0o644, content: tfOutputsTemplate(cloud)},
		{name: "cloud-init.sh", mode: 0o755, content: cloudInit},
		{name: "README.md", mode: 0o644, content: tfReadmeTemplate(cloud, b, bundleSubFormat)},
		{name: "terraform.tfvars.example", mode: 0o644, content: tfVarsExampleTemplate(cloud, b)},
	}
	for _, f := range files {
		if err := writeTarFile(t, root+"/"+f.name, []byte(f.content), f.mode); err != nil {
			return err
		}
	}
	return nil
}

// renderInnerDockerBundle builds the same bytes the operator-facing
// Dockerfile/Compose paths return. The terraform path needs them on
// the parent (in-cache) rather than on disk. Compose is preferred —
// faster VM boot — with dockerfile as the fallback.
func renderInnerDockerBundle(b bundleVars, cfg CPBundleConfig) ([]byte, BundleFormat, error) {
	var buf strings.Builder
	_ = buf // keep linter happy if writeXxx ever changes signature
	if cfg.LinuxImageTarPath != "" {
		buffer := newGrowBuffer()
		if err := writeComposeBundle(buffer, b, cfg.LinuxImageTarPath); err != nil {
			return nil, "", err
		}
		return buffer.Bytes(), BundleFormatCompose, nil
	}
	if cfg.LinuxBinaryPath != "" {
		buffer := newGrowBuffer()
		if err := writeDockerfileBundle(buffer, b, cfg.LinuxBinaryPath); err != nil {
			return nil, "", err
		}
		return buffer.Bytes(), BundleFormatDockerfile, nil
	}
	return nil, "", fmt.Errorf("parent has neither LinuxImageTarPath nor LinuxBinaryPath configured")
}

// growBuffer is a minimal io.Writer that grows like bytes.Buffer.
// We keep it private to avoid pulling bytes into this file when a
// 30-line shim suffices.
type growBuffer struct{ buf []byte }

func newGrowBuffer() *growBuffer { return &growBuffer{} }
func (g *growBuffer) Write(p []byte) (int, error) {
	g.buf = append(g.buf, p...)
	return len(p), nil
}
func (g *growBuffer) Bytes() []byte { return g.buf }

// ── Terraform templates ────────────────────────────────────────────

func tfVersionsTemplate(cloud string) string {
	switch cloud {
	case "oci":
		return `terraform {
  required_version = ">= 1.5.0"
  required_providers {
    oci = {
      source  = "oracle/oci"
      version = ">= 5.0.0"
    }
  }
}
`
	case "aws":
		return `terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0.0"
    }
  }
}
`
	}
	return "# unsupported cloud\n"
}

func tfVariablesTemplate(cloud string, b bundleVars) string {
	switch cloud {
	case "oci":
		return fmt.Sprintf(`# Generated by parent CP %s on %s.
# Operator-supplied knobs. Required values must be set in
# terraform.tfvars (see terraform.tfvars.example).

variable "tenancy_ocid" {
  type        = string
  description = "OCID of the tenancy that owns the compartment."
}

variable "user_ocid" {
  type        = string
  description = "OCID of the IAM user the API key belongs to."
}

variable "fingerprint" {
  type        = string
  description = "API-key fingerprint (e.g. xx:xx:...:xx) for the user_ocid."
}

variable "private_key_path" {
  type        = string
  description = "Path to the PEM private key matching `+"`fingerprint`"+`."
}

variable "region" {
  type        = string
  description = "OCI region the instance launches in."
  default     = %q
}

variable "compartment_id" {
  type        = string
  description = "OCID of the compartment to launch the instance in."
}

variable "availability_domain" {
  type        = string
  description = "AD name (e.g. Uocm:US-ASHBURN-AD-1)."
}

variable "subnet_id" {
  type        = string
  description = "OCID of the subnet to attach the primary VNIC to."
}

variable "image_id" {
  type        = string
  description = "OCID of the source image (Oracle Linux 8 / Ubuntu 22.04 etc.)."
}

variable "shape" {
  type        = string
  description = "Compute shape. Default is the smallest E4.Flex commonly available."
  default     = "VM.Standard.E4.Flex"
}

variable "ocpus" {
  type        = number
  description = "OCPU count for flex shapes. Ignored for fixed shapes."
  default     = 1
}

variable "memory_in_gbs" {
  type        = number
  description = "Memory in GB for flex shapes. Ignored for fixed shapes."
  default     = 8
}

variable "ssh_authorized_keys" {
  type        = string
  description = "Public SSH key(s) to install for the default user. One per line."
  default     = ""
}

variable "assign_public_ip" {
  type        = bool
  description = "Whether the primary VNIC gets a public IP. Set false only if you have NAT egress to the parent CP."
  default     = true
}
`, b.ParentURL, b.IssuedAt, b.Region)
	case "aws":
		return fmt.Sprintf(`# Generated by parent CP %s on %s.
# Operator-supplied knobs. Required values must be set in
# terraform.tfvars (see terraform.tfvars.example).

variable "region" {
  type        = string
  description = "AWS region to launch the instance in."
  default     = %q
}

variable "ami_id" {
  type        = string
  description = "AMI id (Debian / Ubuntu / Amazon Linux 2023 — anything with apt or dnf)."
}

variable "instance_type" {
  type        = string
  description = "EC2 instance type."
  default     = "t3.small"
}

variable "subnet_id" {
  type        = string
  description = "Subnet id the instance attaches to."
}

variable "security_group_ids" {
  type        = list(string)
  description = "VPC security groups attached to the primary ENI."
}

variable "key_name" {
  type        = string
  description = "Optional EC2 key pair name. Empty disables SSH key injection."
  default     = ""
}

variable "iam_instance_profile" {
  type        = string
  description = "Optional IAM instance profile name (not ARN). Empty leaves the instance unprivileged."
  default     = ""
}

variable "assign_public_ip" {
  type        = bool
  description = "Whether the instance gets a public IPv4. Set false only if you have NAT egress to the parent CP."
  default     = true
}

variable "root_volume_gb" {
  type        = number
  description = "Override AMI's default root volume size (GiB). 0 leaves the AMI default."
  default     = 0
}
`, b.ParentURL, b.IssuedAt, b.Region)
	}
	return "# unsupported cloud\n"
}

func tfMainTemplate(cloud string, b bundleVars) string {
	slug := slugify(b.DisplayName)
	switch cloud {
	case "oci":
		return fmt.Sprintf(`provider "oci" {
  tenancy_ocid     = var.tenancy_ocid
  user_ocid        = var.user_ocid
  fingerprint      = var.fingerprint
  private_key_path = var.private_key_path
  region           = var.region
}

resource "oci_core_instance" "okesu_cp" {
  compartment_id      = var.compartment_id
  availability_domain = var.availability_domain
  shape               = var.shape
  display_name        = %q

  shape_config {
    ocpus         = var.ocpus
    memory_in_gbs = var.memory_in_gbs
  }

  source_details {
    source_type = "image"
    source_id   = var.image_id
  }

  create_vnic_details {
    subnet_id        = var.subnet_id
    assign_public_ip = var.assign_public_ip
  }

  metadata = {
    user_data           = filebase64("${path.module}/cloud-init.sh")
    ssh_authorized_keys = var.ssh_authorized_keys
  }

  freeform_tags = {
    "okesu:role"         = "control-plane"
    "okesu:display_name" = %q
    "okesu:region"       = var.region
  }
}
`,
			"okesu-cp-"+slug,
			b.DisplayName,
		)
	case "aws":
		return fmt.Sprintf(`provider "aws" {
  region = var.region
}

resource "aws_instance" "okesu_cp" {
  ami                         = var.ami_id
  instance_type               = var.instance_type
  subnet_id                   = var.subnet_id
  vpc_security_group_ids      = var.security_group_ids
  associate_public_ip_address = var.assign_public_ip
  user_data_base64            = filebase64("${path.module}/cloud-init.sh")
  key_name                    = var.key_name == "" ? null : var.key_name
  iam_instance_profile        = var.iam_instance_profile == "" ? null : var.iam_instance_profile

  dynamic "root_block_device" {
    for_each = var.root_volume_gb > 0 ? [1] : []
    content {
      volume_size           = var.root_volume_gb
      volume_type           = "gp3"
      delete_on_termination = true
    }
  }

  tags = {
    Name           = %q
    "okesu:role"   = "control-plane"
    "okesu:region" = var.region
  }
}
`,
			"okesu-cp-"+slug,
		)
	}
	return "# unsupported cloud\n"
}

func tfOutputsTemplate(cloud string) string {
	switch cloud {
	case "oci":
		return `output "instance_id" {
  description = "OCID of the launched compute instance."
  value       = oci_core_instance.okesu_cp.id
}

output "public_ip" {
  description = "Public IP attached to the primary VNIC (empty when assign_public_ip=false)."
  value       = oci_core_instance.okesu_cp.public_ip
}

output "private_ip" {
  description = "Primary VNIC's private IP."
  value       = oci_core_instance.okesu_cp.private_ip
}

output "ui_url" {
  description = "URL the child CP serves its UI on once cloud-init finishes."
  value       = oci_core_instance.okesu_cp.public_ip == "" ? "" : "https://${oci_core_instance.okesu_cp.public_ip}:8443"
}
`
	case "aws":
		return `output "instance_id" {
  description = "EC2 instance id."
  value       = aws_instance.okesu_cp.id
}

output "public_ip" {
  description = "Public IPv4 (empty when assign_public_ip=false)."
  value       = aws_instance.okesu_cp.public_ip
}

output "private_ip" {
  description = "Private IPv4."
  value       = aws_instance.okesu_cp.private_ip
}

output "ui_url" {
  description = "URL the child CP serves its UI on once cloud-init finishes."
  value       = aws_instance.okesu_cp.public_ip == "" ? "" : "https://${aws_instance.okesu_cp.public_ip}:8443"
}
`
	}
	return "# unsupported cloud\n"
}

func tfVarsExampleTemplate(cloud string, b bundleVars) string {
	switch cloud {
	case "oci":
		return fmt.Sprintf(`# Copy to terraform.tfvars and fill in. DO NOT COMMIT — this file
# does not contain the bootstrap token (it's already baked into
# cloud-init.sh) but it does contain your tenancy + user OCIDs.

tenancy_ocid     = "ocid1.tenancy.oc1..xxx"
user_ocid        = "ocid1.user.oc1..xxx"
fingerprint      = "aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"
private_key_path = "~/.oci/oci_api_key.pem"
region           = %q

compartment_id      = "ocid1.compartment.oc1..xxx"
availability_domain = "Uocm:US-ASHBURN-AD-1"
subnet_id           = "ocid1.subnet.oc1..xxx"
image_id            = "ocid1.image.oc1..xxx"

# Optional overrides
# shape          = "VM.Standard.E4.Flex"
# ocpus          = 1
# memory_in_gbs  = 8
# assign_public_ip = true
# ssh_authorized_keys = file("~/.ssh/id_ed25519.pub")
`, b.Region)
	case "aws":
		return fmt.Sprintf(`# Copy to terraform.tfvars and fill in. DO NOT COMMIT — this file
# does not contain the bootstrap token (it's already baked into
# cloud-init.sh).

region              = %q
ami_id              = "ami-xxxxxxxxxxxxxxxxx"
subnet_id           = "subnet-xxxxxxxx"
security_group_ids  = ["sg-xxxxxxxx"]

# Optional overrides
# instance_type        = "t3.small"
# key_name             = "my-keypair"
# iam_instance_profile = "my-instance-profile"
# assign_public_ip     = true
# root_volume_gb       = 0
`, b.Region)
	}
	return "# unsupported cloud\n"
}

func tfReadmeTemplate(cloud string, b bundleVars, inner BundleFormat) string {
	var sb strings.Builder
	sb.WriteString("# okesu-cp child Terraform module — `" + b.DisplayName + "`\n\n")
	sb.WriteString("Generated by parent at `" + b.ParentURL + "` on " + b.IssuedAt + ".\n")
	sb.WriteString("Cloud: **" + strings.ToUpper(cloud) + "**\n\n")
	sb.WriteString("## What this is\n\n")
	sb.WriteString("A self-contained Terraform module that launches one VM in your\n")
	sb.WriteString("cloud account. The VM cloud-inits itself by fetching the parent CP's\n")
	sb.WriteString("Docker bundle, running `docker compose up -d`, and registering with\n")
	sb.WriteString("the parent. End state: a new child CP at this parent, region `" + b.Region + "`.\n\n")
	sb.WriteString("Inner bundle format: `" + string(inner) + "` — the cloud-init script\n")
	sb.WriteString("knows how to extract and run it.\n\n")

	sb.WriteString("## Run\n\n")
	sb.WriteString("```sh\n")
	sb.WriteString("tar -xzf okesu-cp-*.tar.gz\n")
	sb.WriteString("cd " + bundleRootDir(b) + "\n")
	sb.WriteString("cp terraform.tfvars.example terraform.tfvars\n")
	sb.WriteString("# edit terraform.tfvars with your values\n")
	sb.WriteString("terraform init\n")
	sb.WriteString("terraform apply\n")
	sb.WriteString("```\n\n")
	sb.WriteString("`terraform apply` will:\n\n")
	sb.WriteString("1. Authenticate to your cloud account using the credentials in `terraform.tfvars`.\n")
	sb.WriteString("2. Create one VM with `cloud-init.sh` as user-data.\n")
	sb.WriteString("3. The VM installs Docker, pulls the bundle from `" + b.ParentURL + "/api/federation/cp-bundle/download`,\n")
	sb.WriteString("   and runs `docker compose up -d`.\n")
	sb.WriteString("4. The child CP POSTs to the parent's `/api/v1/cp/bootstrap` and joins the federation.\n\n")

	sb.WriteString("## Variables\n\n")
	sb.WriteString("See `variables.tf` for the full list. Required:\n\n")
	switch cloud {
	case "oci":
		sb.WriteString("- `tenancy_ocid`, `user_ocid`, `fingerprint`, `private_key_path` — your OCI API auth\n")
		sb.WriteString("- `compartment_id`, `availability_domain`, `subnet_id`, `image_id` — where the VM lands\n")
	case "aws":
		sb.WriteString("- `ami_id`, `subnet_id`, `security_group_ids` — where the VM lands\n")
		sb.WriteString("- AWS credentials are picked up from your shell's standard env / config (the module does not embed them)\n")
	}
	sb.WriteString("\n")

	sb.WriteString("## Login\n\n")
	sb.WriteString("Once `terraform apply` finishes:\n\n")
	sb.WriteString("- URL:      see the `ui_url` output (`https://<public_ip>:" + fmt.Sprintf("%d", b.ChildPort) + "/`)\n")
	sb.WriteString("- Email:    `admin@local`\n")
	sb.WriteString("- Password: `" + b.AdminPassword + "` (also baked into the bundle's `.env` inside the VM)\n\n")
	sb.WriteString("Change the admin password after the first login.\n\n")

	sb.WriteString("## Security notes\n\n")
	sb.WriteString("- **Bootstrap token is one-time use** and expires 24h after `" + b.IssuedAt + "`.\n")
	sb.WriteString("  After it's burned (the child CP successfully registers) or expires, you'll\n")
	sb.WriteString("  need to regenerate this module to launch another child.\n")
	sb.WriteString("- The token is baked into `cloud-init.sh`. Treat the entire module dir as\n")
	sb.WriteString("  sensitive until `terraform destroy` or token expiry — do not commit it.\n")
	sb.WriteString("- `terraform.tfvars` is in `.gitignore` by Terraform convention; the example\n")
	sb.WriteString("  file does not contain the token.\n")
	sb.WriteString("- The bundle download endpoint is gated by the bootstrap token; once burned,\n")
	sb.WriteString("  even an attacker holding the cloud-init script can't refetch.\n")
	return sb.String()
}
