resource "docker_container" "prometheus" {
  name  = "prometheus"
  image = docker_image.prometheus.latest
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
  volumes {
    volume_name    = "prometheus-data"
    container_path = "/prometheus-data"
  }
  volumes {
    host_path      = abspath("cache/prometheus.yml")
    container_path = "/etc/prometheus/prometheus.yml"
    read_only      = true
  }
  network_mode = "bridge"
  networks_advanced {
    name         = docker_network.devconsul-lan.name
    ipv4_address = "10.0.100.100"
  }

  ports {
    internal = 9090
    external = 9090
  }
  ports {
    internal = 3000
    external = 3000
  }
}
