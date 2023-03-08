active = "prometheus"

config "prometheus" {
  consul_image = "consul-dev:latest"

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

  monitor {
    prometheus = true
  }

  envoy {
    # log_level = "debug"
    # log_level = "trace"
    log_level = "info"
  }

  config_entries = [
    <<EOF
{
    "Kind": "proxy-defaults",
    "Name": "global",
    "Config": {
        "protocol": "http"
    },
    "MeshGateway": {
        "Mode": "local"
    }
}
EOF
    ,
    # <<EOF
    # {
    #   "Kind": "service-resolver",
    #   "Name": "pong",
    #   "Redirect": {
    #       "Datacenter": "dc2"
    #   }
    # }
    # EOF
    # ,
    # <<EOF
    # {
    #   "Kind": "service-resolver",
    #   "Name": "ping",
    #   "Subsets": {
    #       "v1": {
    #           "Filter": "Service.Meta.version == v1"
    #       },
    #       "v2": {
    #           "Filter": "Service.Meta.version == v2"
    #       }
    #   }
    # }
    # EOF
    # ,
    # <<EOF
    # {
    #   "Kind": "service-splitter",
    #   "Name": "ping",
    #   "Splits": [
    #       {
    #           "Weight": 50,
    #           "ServiceSubset": "v1"
    #       },
    #       {
    #           "Weight": 50,
    #           "ServiceSubset": "v2"
    #       }
    #   ]
    # }
    # EOF
    # ,
  ]

  topology {
    network_shape = "flat"

    cluster "dc1" {
      servers = 1
      clients = 5
      # Gateways are the last client in each DC.
      mesh_gateways = 1
    }
    cluster "dc2" {
      servers = 1
      clients = 5
      # Gateways are the last client in each DC.
      mesh_gateways = 1
    }

    node "dc1-client1" {
      service_meta = {
        version = "v1" // ping
      }

      upstream_extra_hcl = <<EOF
          config {
              protocol = "grpc"
          }
EOF
    }

    node "dc1-client2" {
      service_meta = {
        version = "v1" // pong
      }
    }

    node "dc1-client3" {
      service_meta = {
        version = "v2" // ping
      }
    }

    node "dc1-client4" {
      service_meta = {
        version = "v2" // pong
      }
    }

    node "dc2-client1" {
      service_meta = {
        version = "v1" // ping
      }
    }

    node "dc2-client2" {
      service_meta = {
        version = "v1" // pong
      }
    }

    node "dc2-client3" {
      service_meta = {
        version = "v2" // ping
      }
    }

    node "dc2-client4" {
      service_meta = {
        version = "v2" // pong
      }
    }
  }
}
