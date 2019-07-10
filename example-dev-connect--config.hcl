consul_image = "consul-dev:latest"

initial_master_token = "root"

encryption {
  tls    = true
  gossip = true
}

kubernetes {
  enabled = false
}

envoy {
  log_level = "debug"

  # log_level = "trace"
}

topology {
  servers {
    dc1 = 1
    dc2 = 1
  }

  clients {
    dc1 = 2
    dc2 = 2
  }
}
