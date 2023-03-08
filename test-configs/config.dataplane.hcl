active = "simple"

config "simple" {
  consul_image    = "consul-dev:latest"
  dataplane_image = "hashicorp/consul-dataplane:1.0.0"

  security {
    initial_master_token = "root"
    encryption {
      tls             = true
      tls_grpc        = true
      gossip          = true
      server_tls_grpc = true
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

    ### default toggle
    # node_mode = "dataplane"

    node "dc1-client1" {
      mode = "dataplane"
    }
  }
}
