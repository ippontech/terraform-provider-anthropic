resource "anthropic_file" "example" {
  source_path = "${path.module}/example.txt"
}

data "anthropic_file" "example" {
  id = anthropic_file.example.id
}

output "file_filename" {
  description = "Filename recorded for the file."
  value       = data.anthropic_file.example.filename
}

output "file_size_bytes" {
  description = "Size of the file in bytes."
  value       = data.anthropic_file.example.size_bytes
}

output "file_id" {
  description = "ID of the file read back by the data source."
  value       = data.anthropic_file.example.id
}
