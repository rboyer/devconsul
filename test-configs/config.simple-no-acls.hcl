active = "simple-no-acls"

config "simple-no-acls" {
  consul_image  = "consul-dev:latest"
  envoy_version = "v1.22.5"

  security {
    disable_acls         = true
    initial_master_token = "root"
    encryption {
      tls    = true
      gossip = true
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
