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
