resource "kubernetes_namespace_v1" "cloud_ns" {
  metadata {
    name = var.namespace
  }
}

data "external" "jwt_signing_key" {
  program = ["bash", "${path.module}/../scripts/create_random_bytes.sh", "a-zA-Z0-9", "64"]
}

resource "kubernetes_secret_v1" "cloud_auth" {
  metadata {
    name      = "cloud-auth-secrets"
    namespace = kubernetes_namespace_v1.cloud_ns.metadata.0.name
  }

  data = {
    "jwt-signing-key" = data.external.jwt_signing_key.result["output"]
  }

  type                           = "Opaque"
  wait_for_service_account_token = false

  lifecycle {
    ignore_changes = [
      data["jwt-signing-key"],
    ]
  }
}

data "external" "session_key" {
  program = ["bash", "${path.module}/../scripts/create_random_bytes.sh", "a-zA-Z0-9", "24"]
}

resource "kubernetes_secret_v1" "cloud_session" {
  metadata {
    name      = "cloud-session-secrets"
    namespace = kubernetes_namespace_v1.cloud_ns.metadata.0.name
  }

  data = {
    "session-key" = data.external.session_key.result["output"]
  }

  type                           = "Opaque"
  wait_for_service_account_token = false

  lifecycle {
    ignore_changes = [
      data["session-key"],
    ]
  }
}

data "external" "db_key" {
  program = ["bash", "${path.module}/../scripts/create_random_bytes.sh", "a-zA-Z0-9#$%&().", "24"]
}

data "external" "postgres_password" {
  program = ["bash", "${path.module}/../scripts/create_random_bytes.sh", "a-zA-Z0-9", "24"]
}

resource "kubernetes_secret_v1" "db_secrets" {
  metadata {
    name      = "pl-db-secrets"
    namespace = kubernetes_namespace_v1.cloud_ns.metadata.0.name
  }

  data = {
    "PL_POSTGRES_USERNAME" = "pl"
    "PL_POSTGRES_PASSWORD" = data.external.postgres_password.result["output"]
    "database-key"         = data.external.db_key.result["output"]
  }

  lifecycle {
    ignore_changes = [
      data["database-key"],
      data["PL_POSTGRES_PASSWORD"],
    ]
  }

  type                           = "Opaque"
  wait_for_service_account_token = false
}

# TODO(ddelnano): Ressurect if this is needed for the cert validation method we settle on
# resource "kubernetes_secret_v1" "cert_manager_sa" {
#   metadata {
#     name      = "cert-manager-dns01-solver-svc-acct"
#     namespace = kubernetes_namespace_v1.cloud_ns.metadata.0.name
#   }

#   data = {
#     "key.json" = base64decode(data.terraform_remote_state.k8s.outputs.cert_manager_sa_credentials_file)
#   }

#   type                           = "Opaque"
#   wait_for_service_account_token = false
# }

data "sops_file" "auth0" {
  source_file = "${path.module}/../../credentials/cockpit/auth0_config.yaml"
}

resource "kubernetes_secret_v1" "cloud_auth0" {
  metadata {
    name      = "cloud-auth0-secrets"
    namespace = kubernetes_namespace_v1.cloud_ns.metadata.0.name
  }

  data = {
    "auth0-client-id"     = data.sops_file.auth0.data["stringData.auth0-client-id"]
    "auth0-client-secret" = data.sops_file.auth0.data["stringData.auth0-client-secret"]
  }

  type                           = "Opaque"
  wait_for_service_account_token = false
}

# TODO(ddelnano): Must replace this if the public override isn't used.
# resource "kubernetes_config_map_v1" "db_config" {
#   metadata {
#     name      = "pl-db-config"
#     namespace = kubernetes_namespace_v1.cloud_ns.metadata.0.name
#     labels = {
#       "app" = "pl-cloud"
#     }
#   }

#   data = {
#     "PL_POSTGRES_DB"       = data.terraform_remote_state.cloudsql.outputs.db_name
#     "PL_POSTGRES_HOSTNAME" = "localhost"
#     "PL_POSTGRES_PORT"     = "5432"
#     "PL_POSTGRES_INSTANCE" = data.terraform_remote_state.cloudsql.outputs.db_connection_name
#   }
# }
