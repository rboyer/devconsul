active = "simple"
# active = "mesh-gateways"
# active = "wan-federation-via-mesh-gateways"

config "simple" {
  consul_image = "consul-dev:latest"
  # consul_image = "consul:1.6.1"

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
    # log_level = "trace"
    # log_level = "info"
    log_level = "debug"
  }

  topology {
    datacenter "dc1" {
      servers = 1
      clients = 2
    }
    datacenter "dc2" {
      servers = 1
      clients = 2
    }
    datacenter "dc3" {
      servers = 1
      clients = 2
    }
  }
}

config "mesh-gateways" {
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

  envoy {
    # log_level = "trace"
    # log_level = "info"
    log_level = "debug"
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
    datacenter "dc1" {
      servers = 1
      clients = 4
      # Gateways are the last client in each DC.
      # mesh_gateways = 1
    }
    datacenter "dc2" {
      servers = 1
      clients = 4
      # Gateways are the last client in each DC.
      # mesh_gateways = 1
    }
    datacenter "dc3" {
      servers = 1
      clients = 4
      # Gateways are the last client in each DC.
      # mesh_gateways = 1
    }

    node "dc1-client1" {
      service_meta = {
        version = "v1" // ping
      }
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

config "wan-federation-via-mesh-gateways" {
  consul_image = "consul-dev:latest"

  security {
    initial_master_token = "root"
    encryption {
      tls    = true
      gossip = true
    }
  }

  envoy {
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
  ]

  topology {
    network_shape = "islands"
    # network_shape = "dual"
    # network_shape = "flat"

    datacenter "dc1" {
      servers = 3
      clients = 2
      # Gateways are the last client in each DC.
      mesh_gateways = 1
    }
    datacenter "dc2" {
      servers = 3
      clients = 2
      # Gateways are the last client in each DC.
      mesh_gateways = 1
    }
  }
}
