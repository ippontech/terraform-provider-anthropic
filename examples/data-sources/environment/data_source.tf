# Create an environment to read with the data source
resource "anthropic_environment" "created" {
  name = "environment-data-source-example"
}

# Look up the created environment by ID
data "anthropic_environment" "example" {
  environment_id = anthropic_environment.created.id
}

output "environment_id" {
  description = "ID of the environment."
  value       = data.anthropic_environment.example.id
}

output "environment_name" {
  description = "Name of the environment."
  value       = data.anthropic_environment.example.name
}

output "environment_type" {
  description = "Type of the environment."
  value       = data.anthropic_environment.example.type
}
