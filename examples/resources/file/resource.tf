resource "anthropic_file" "example" {
  source_path = "${path.module}/example.txt"
}

output "file_id" {
  description = "ID of the uploaded file."
  value       = anthropic_file.example.id
}
