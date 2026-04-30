terraform {
  required_version = ">= 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 0.1.0"
    }
  }
}

# Minimal environment
resource "anthropic_environment" "minimal" {
  name = "minimal-environment"
}

# Environment with limited network and pip packages
resource "anthropic_environment" "python_data" {
  name        = "python-data-analysis"
  description = "Environment for Python data analysis workloads"

  metadata = {
    team = "data-science"
    env  = "production"
  }

  config = {
    networking = {
      type                   = "limited"
      allow_package_managers = true
      allowed_hosts          = ["api.example.com"]
    }
    packages = {
      pip = ["pandas", "numpy"]
    }
  }
}

# Unrestricted environment
resource "anthropic_environment" "unrestricted" {
  name = "unrestricted-environment"

  config = {
    networking = {
      type = "unrestricted"
    }
  }
}

output "environment_id" {
  value = anthropic_environment.python_data.id
}

output "environment_type" {
  value = anthropic_environment.python_data.type
}

output "environment_networking_type" {
  value = anthropic_environment.python_data.config.networking.type
}
