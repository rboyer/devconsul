terraform {
  required_providers {
    docker = {
      source  = "terraform-providers/docker"
      version = "2.7.2"
    }
  }
  required_version = ">= 0.13"
}
