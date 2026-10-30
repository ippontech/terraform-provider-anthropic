variable "collector_api_key" {
  description = "API key the observability platform's OTLP endpoint authenticates deliveries with."
  type        = string
  sensitive   = true
  ephemeral   = true
}

resource "anthropic_telemetry_destination" "example" {
  display_name = "Acme observability platform"
  endpoint_url = "https://gateway.example.com/otlp"

  # Write-only: sent to the API, never stored in state. Change
  # headers_wo_version to send new values.
  headers_wo = {
    authorization = "Bearer ${var.collector_api_key}"
  }
  headers_wo_version = 1

  workspaces = {
    type          = "include"
    workspace_ids = ["wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"]
  }

  traces = {
    content_mode      = "content"
    max_content_bytes = 786432
  }
}

output "telemetry_destination_id" {
  description = "ID of the telemetry destination."
  value       = anthropic_telemetry_destination.example.id
}

output "telemetry_destination_header_names" {
  description = "Names of the headers registered on the destination (their values are never returned)."
  value       = anthropic_telemetry_destination.example.header_names
}
