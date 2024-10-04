resource "docker_container" "vault" {
  name  = "vault"
  image = docker_image.vault.latest
  labels {
    label = "devconsul"
    value = "1"
  }
  labels {
    label = "devconsul.type"
    value = "infra"
  }
  restart = "always"
  dns     = ["8.8.8.8"]
  env     = ["SKIP_SETCAP=1", "VAULT_CLUSTER_INTERFACE=eth0"]

  command = ["server"]

  volumes {
    volume_name    = "vault-data"
    container_path = "/vault/file"
  }

  volumes {
    host_path      = abspath("cache/vault-config.hcl")
    container_path = "/vault/config/config.hcl"
    read_only      = true
  }

  network_mode = "bridge"
  networks_advanced {
    name         = docker_network.devconsul-lan.name
    ipv4_address = "10.0.100.111"
  }

  ports {
    internal = 8200
    external = 8200
  }
}
