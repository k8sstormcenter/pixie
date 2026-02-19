locals {
  cert_subdomain = var.cloud_domain
  cert_config    = var.cert_details[var.cloud_domain]
}

resource "kubernetes_manifest" "clusterissuer_selfsigned_issuer" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "ClusterIssuer"
    "metadata" = {
      "name" = "selfsigned-issuer"
    }
    "spec" = {
      "selfSigned" = {}
    }
  }
}

resource "kubernetes_manifest" "ca" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Certificate"
    "metadata" = {
      "name"      = "pixie-cloud-ca"
      "namespace" = kubernetes_namespace_v1.cloud_ns.metadata.0.name
    }
    "spec" = {
      "commonName" = local.cert_config.ca_common_name
      "duration"   = "131400h"
      "isCA"       = true
      "issuerRef" = {
        "group" = "cert-manager.io"
        "kind"  = "ClusterIssuer"
        "name"  = "selfsigned-issuer"
      }
      "privateKey" = {
        "algorithm" = "ECDSA"
        "size"      = 256
      }
      "secretName" = "root-secret"
      "subject" = {
        "organizations" = local.cert_config.organizations
        "serialNumber"  = "1"
      }
    }
  }

  lifecycle {
    precondition {
      condition     = contains(keys(var.cert_details), var.cloud_domain)
      error_message = "cloud_domain '${var.cloud_domain}' must have a corresponding entry in cert_details."
    }
  }
}

resource "kubernetes_manifest" "issuer_ca_issuer" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Issuer"
    "metadata" = {
      "name"      = "pixie-cloud-ca-issuer"
      "namespace" = kubernetes_namespace_v1.cloud_ns.metadata.0.name
    }
    "spec" = {
      "ca" = {
        "secretName" = "root-secret"
      }
    }
  }
}

# TODO(ddelnano): This needs to have an issuer for the production cert.
# This will likely be a DNS01 challenge / AzureDNS
resource "kubernetes_manifest" "cloud_proxy_tls_certs" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Certificate"
    "metadata" = {
      "name"      = "cloud-proxy-tls-certs"
      "namespace" = kubernetes_namespace_v1.cloud_ns.metadata.0.name
    }
    "spec" = {
      "dnsNames" = [
        local.cert_subdomain,
        "*.${local.cert_subdomain}",
      ]
      "duration" = "43800h"
      "issuerRef" = {
        "name" = "pixie-cloud-ca-issuer"
      }
      "secretName" = "cloud-proxy-tls-certs"
      "subject" = {
        "countries" = [
          "US",
        ]
        "localities" = [
          "San Francisco",
        ]
        "organizations" = local.cert_config.organizations
        "provinces" = [
          "California",
        ]
      }
    }
  }
}

resource "kubernetes_manifest" "service_tls_server_certs" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Certificate"
    "metadata" = {
      "name"      = "service-tls-server"
      "namespace" = kubernetes_namespace_v1.cloud_ns.metadata.0.name
    }
    "spec" = {
      "commonName" = "pixie.server"
      "dnsNames" = [
        "*.${var.namespace}",
        "*.${var.namespace}.svc.cluster.local",
        "*.${var.namespace}.pod.cluster.local",
        "*.pl-nats.${var.namespace}.svc",
        "*.pl-nats",
        "pl-nats",
        "*.local",
        "localhost",
        "kratos",
      ]
      "duration" = "43800h"
      "issuerRef" = {
        "name" = "pixie-cloud-ca-issuer"
      }
      "privateKey" = {
        "algorithm" = "RSA"
        "size"      = 4096
      }
      "secretName" = "service-tls-server-certs"
      "subject" = {
        "organizations" = local.cert_config.organizations
      }
    }
  }
}

resource "kubernetes_manifest" "service_tls_client_certs" {
  manifest = {
    "apiVersion" = "cert-manager.io/v1"
    "kind"       = "Certificate"
    "metadata" = {
      "name"      = "service-tls-client"
      "namespace" = kubernetes_namespace_v1.cloud_ns.metadata.0.name
    }
    "spec" = {
      "commonName" = "pixie.client"
      "dnsNames" = [
        "*.${var.namespace}",
        "*.${var.namespace}.svc.cluster.local",
        "*.${var.namespace}.pod.cluster.local",
        "*.pl-nats.${var.namespace}.svc",
        "*.pl-nats",
        "pl-nats",
        "*.local",
        "localhost",
        "kratos",
      ]
      "duration" = "43800h"
      "issuerRef" = {
        "name" = "pixie-cloud-ca-issuer"
      }
      "privateKey" = {
        "algorithm" = "RSA"
        "size"      = 4096
      }
      "secretName" = "service-tls-client-certs"
      "subject" = {
        "organizations" = local.cert_config.organizations
      }
      "usages" = [
        "client auth",
        "digital signature",
        "key encipherment",
      ]
    }
  }
}
