variable "region" {
  default = "us-west1"
}
variable "project_environment" {
  default = "prod"
}
variable "cluster_env" {
  default = "prod"
}

variable "cluster_name" {
  default = "prod-pixie-cloud"
}
variable "namespace" {
  default = "plc"
}

variable "cloud_domain" {
  default = "pixie.austrianopencloudcommunity.org"
}

variable "cert_details" {
  type = map(object({
    ca_common_name = string
    organizations  = list(string)
  }))
  default = {
    "pixie.austrianopencloudcommunity.org" = {
      ca_common_name = "Cosmic Observe, Inc."
      organizations  = ["Cosmic"]
    }
  }
}

variable "cluster_internal_issuer" {
  default = "pixie-cloud-ca-issuer"
}

variable "public_issuer" {
  default = "letsencrypt-prod"
}

# Auth0 remote state lookup — reads pixie_client_id / pixie_client_secret
# outputs from the auth0 terraform state. All four must be supplied by the
# caller (pipeline passes them as -var).
variable "auth0_state_resource_group" {
  type = string
}
variable "auth0_state_storage_account" {
  type = string
}
variable "auth0_state_container" {
  type    = string
  default = "tfoscaas-0001"
}
variable "auth0_state_key" {
  type    = string
  default = "auth0-ckp2.tfstate"
}
