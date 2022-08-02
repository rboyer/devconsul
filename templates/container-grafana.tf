resource "docker_container" "grafana" {
  name  = "grafana"
  image = docker_image.grafana.latest
  labels {
    label = "devconsul"
    value = "1"
  }
  labels {
    label = "devconsul.type"
    value = "infra"
  }
  restart      = "always"
  network_mode = "container:${docker_container.prometheus.id}"
  volumes {
    volume_name    = "grafana-data"
    container_path = "/var/lib/grafana"
  }
  volumes {
    host_path      = abspath("cache/grafana-prometheus.yml")
    container_path = "/etc/grafana/provisioning/datasources/prometheus.yml"
    read_only      = true
  }
  volumes {
    host_path      = abspath("cache/grafana.ini")
    container_path = "/etc/grafana/grafana.ini"
    read_only      = true
  }
}
