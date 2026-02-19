terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "2.30.0"
    }
    kustomization = {
      source  = "kbst/kustomization"
      version = "0.9.7"
    }
    sops = {
      source  = "carlpett/sops"
      version = "~> 1.0"
    }
  }
}

provider "kubernetes" {
  config_path    = "~/.kube/cockpick-config"
  config_context = "default"
}

provider "kustomization" {
  context = "default"
  kubeconfig_path = "~/.kube/cockpick-config"
}
