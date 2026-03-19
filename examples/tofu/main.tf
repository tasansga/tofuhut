terraform {
  required_version = ">= 1.6.0"
}

# Minimal OpenTofu example that has no external provider dependencies.
output "tofuhut_example" {
  value = "hello from tofu example"
}
