# A key in AWS KMS. Its key policy must grant Anthropic's role access first
# (see the CMEK setup guide); `region` is derived from the ARN when omitted.
resource "anthropic_external_key" "example" {
  display_name = "prod-us-key"
  geo          = "us"

  provider_config = {
    type    = "aws"
    kms_arn = "arn:aws:kms:us-east-2:111122223333:key/abcd1234-5678-90ab-cdef-000011112222"
  }
}

# A key in Google Cloud KMS, by its full resource name.
resource "anthropic_external_key" "gcp" {
  display_name = "prod-us-key-gcp"

  provider_config = {
    type     = "gcp"
    key_name = "projects/my-project/locations/us-east1/keyRings/anthropic/cryptoKeys/anthropic-cmek"
  }
}

# A key in Azure Key Vault. Omit `client_id` to use Anthropic's multitenant app.
resource "anthropic_external_key" "azure" {
  display_name = "prod-us-key-azure"

  provider_config = {
    type      = "azure"
    vault_uri = "https://my-vault.vault.azure.net"
    key_name  = "anthropic-cmek"
    tenant_id = "00000000-0000-0000-0000-000000000000"
  }
}

# Attaching a key to a workspace cannot be undone: the workspace keeps it for
# good, the key's provider_config freezes and the key can no longer be deleted.
#
# resource "anthropic_workspace" "encrypted" {
#   name            = "Encrypted Workspace"
#   external_key_id = anthropic_external_key.example.id
# }

output "external_key_id" {
  description = "ID of the AWS external key, to set as a workspace's external_key_id."
  value       = anthropic_external_key.example.id
}

output "external_key_attachment" {
  description = "Whether a workspace uses the AWS key (`attached` or `unattached`)."
  value       = anthropic_external_key.example.attachment
}
