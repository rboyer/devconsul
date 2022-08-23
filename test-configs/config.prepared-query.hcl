active = "prepared-query"

config "prepared-query" {
  consul_image  = "consul-dev:latest"
  envoy_version = "v1.22.5"

  security {
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
    # log_level = "debug"
    # log_level = "info"
    log_level = "trace"
  }

  topology {
    network_shape = "flat"

    cluster "dc1" {
      servers = 1
      clients = 2
    }
    cluster "dc2" {
      servers = 1
      clients = 2
    }

    node "dc1-client1" {
      service_meta = {
        version = "v1" // ping
      }

      // this is hacky, we "append" to the list with this weird use of curly braces
      upstream_extra_hcl = <<EOF
          },
          {
            destination_type = "prepared_query"
            destination_name = "pong-query"
            local_bind_port  = 9191
EOF
    }
  }
}
