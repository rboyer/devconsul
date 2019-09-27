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
  datacenters {
    dc1 {
      servers = 1
      clients = 2
    }
    dc2 {
      servers = 1
      clients = 2
    }
    dc3 {
      servers = 1
      clients = 2
    }
  }
}
