active = "simple"

config "simple" {
  consul_image = "consul-dev:latest"

  security {
    initial_master_token = "root"
    encryption {
      tls    = true
      gossip = true
    }
    vault {
      enabled = true
    }
  }

  kubernetes {
    enabled = false
  }

  envoy {
    # log_level = "trace"
    # log_level = "info"
    log_level = "debug"
  }

  topology {
    cluster "dc1" {
      servers = 1
      clients = 2
    }
    cluster "dc2" {
      servers = 1
      clients = 2
    }
  }
}
