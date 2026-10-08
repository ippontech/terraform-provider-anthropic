resource "anthropic_file" "example" {
  source_path = "${path.module}/example.txt"
}

# Restrict the listing to specific file IDs. Omit `ids` to list every file in
# the workspace.
data "anthropic_files" "example" {
  ids = [anthropic_file.example.id]
}

output "file_ids" {
  description = "IDs of the listed files."
  value       = data.anthropic_files.example.files[*].id
}
