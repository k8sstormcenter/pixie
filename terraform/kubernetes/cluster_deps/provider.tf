terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "2.30.0"
    }
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
  }
}

provider "azurerm" {
  features {}
  subscription_id = "d3178f52-bf32-4360-a534-5f4faa991f62"
}

provider "kubernetes" {
  config_path    = "~/.kube/cockpick-config"
  config_context = "default"
}

provider "helm" {
  kubernetes = {
    config_path    = "~/.kube/cockpick-config"
    config_context = "default"
  }
}
