# CP-side secrets generated at apply time and written to secrets_dir
# where the CP's file:// secrets adapter reads them.

resource "random_password" "admin" {
  length  = 32
  special = true
}

resource "random_password" "session_key" {
  length  = 64
  special = false
}

resource "random_password" "webhook" {
  length  = 32
  special = false
}

resource "local_sensitive_file" "admin" {
  filename        = "${pathexpand(var.secrets_dir)}/cp/admin-password"
  content         = random_password.admin.result
  file_permission = "0600"
}

resource "local_sensitive_file" "session_key" {
  filename        = "${pathexpand(var.secrets_dir)}/cp/session-key"
  content         = random_password.session_key.result
  file_permission = "0600"
}

resource "local_sensitive_file" "webhook" {
  filename        = "${pathexpand(var.secrets_dir)}/cp/webhook-secret"
  content         = random_password.webhook.result
  file_permission = "0600"
}

resource "random_password" "clickhouse" {
  length  = 32
  special = false
}

resource "local_sensitive_file" "clickhouse_password" {
  filename        = "${pathexpand(var.secrets_dir)}/clickhouse/password"
  content         = random_password.clickhouse.result
  file_permission = "0600"
}
