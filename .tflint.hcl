plugin "terraform" {
  enabled = true
  preset  = "all"
}

# Examples are single-purpose modules (resource.tf / data_source.tf + versions.tf)
# embedded in the Registry docs; the standard main/variables/outputs layout does
# not apply to them (#229).
rule "terraform_standard_module_structure" {
  enabled = false
}
