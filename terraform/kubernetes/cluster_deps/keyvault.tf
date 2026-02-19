data "azurerm_client_config" "current" {}

resource "azurerm_resource_group" "pixie_cloud" {
  name     = "E020-04-pixie-cloud"
  location = "westeurope"
}

resource "azurerm_key_vault" "pixie_cloud" {
  name                = "kv-pixie-cloud"
  location            = azurerm_resource_group.pixie_cloud.location
  resource_group_name = azurerm_resource_group.pixie_cloud.name
  tenant_id           = data.azurerm_client_config.current.tenant_id
  sku_name            = "standard"

  purge_protection_enabled   = true
  soft_delete_retention_days = 7

  access_policy {
    tenant_id = data.azurerm_client_config.current.tenant_id
    object_id = data.azurerm_client_config.current.object_id

    key_permissions = [
      "Get",
      "List",
      "Create",
      # Remove Delete and Purge later
      "Delete",
      "Purge",
      "Recover",
      "Encrypt",
      "Decrypt",
      "GetRotationPolicy",
    ]

    secret_permissions = [
      "Get",
      "List",
      "Restore",
      "Delete",
      "Set",
      "Recover",
      "Backup",
    ]
  }
}

resource "azurerm_key_vault_key" "sops" {
  name         = "sops-key"
  key_vault_id = azurerm_key_vault.pixie_cloud.id
  key_type     = "RSA"
  key_size     = 2048

  key_opts = [
    "encrypt",
    "decrypt",
  ]

  depends_on = [azurerm_key_vault.pixie_cloud]
}

output "sops_key_id" {
  description = "The versioned key ID for use in .sops.yaml"
  value       = azurerm_key_vault_key.sops.id
}

output "sops_key_versionless_id" {
  description = "The versionless key ID for use in .sops.yaml"
  value       = azurerm_key_vault_key.sops.versionless_id
}
