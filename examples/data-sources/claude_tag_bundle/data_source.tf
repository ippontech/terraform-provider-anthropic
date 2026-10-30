data "anthropic_claude_tag_bundle" "support_tools" {
  id = "capp_011CZkZjLP1cWxj5EtMmdgbq"
}

output "bundle_name" {
  description = "Name of the Access bundle."
  value       = data.anthropic_claude_tag_bundle.support_tools.display_name
}
