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

config_entries = [
  <<EOF
{
  "Kind": "service-resolver",
  "Name": "pong",
  "Redirect": {
      "Datacenter": "dc2"
  }
}
EOF
  ,
  <<EOF
{
  "Kind": "proxy-defaults",
  "Name": "global",
  "Config": {
      "protocol" : "http"
  },
  "MeshGateway": {
      "Mode": "local"
  }
}
EOF
  ,
]

topology {
  datacenters {
    dc1 {
      servers = 1
      clients = 5
    }
    dc2 {
      servers = 1
      clients = 5
    }
    dc3 {
      servers = 1
      clients = 5
    }
  }

  node_config {
    // Gateways are the last client in each DC.
    "dc1-client5" = {
      mesh_gateway = true
    }

    "dc2-client5" = {
      mesh_gateway = true
    }

    "dc3-client5" = {
      mesh_gateway = true
    }

    "dc1-client1" = {
      service_meta {
        version = "v1" // ping
      }
    }

    "dc1-client2" = {
      service_meta {
        version = "v1" // pong
      }
    }

    "dc1-client3" = {
      service_meta {
        version = "v2" // ping
      }
    }

    "dc1-client4" = {
      service_meta {
        version = "v2" // pong
      }
    }

    "dc2-client1" = {
      service_meta {
        version = "v1" // ping
      }
    }

    "dc2-client2" = {
      service_meta {
        version = "v1" // pong
      }
    }

    "dc2-client3" = {
      service_meta {
        version = "v2" // ping
      }
    }

    "dc2-client4" = {
      service_meta {
        version = "v2" // pong
      }
    }
  }
}
