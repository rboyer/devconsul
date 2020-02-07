consul_image = "consul-dev:latest"

initial_master_token = "root"

encryption {
  tls    = true
  gossip = true
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

  datacenters {
    dc1 {
      servers = 3
      clients = 2
      # Gateways are the last client in each DC.
      mesh_gateways = 1
    }
    dc2 {
      servers = 3
      clients = 2
      # Gateways are the last client in each DC.
      mesh_gateways = 1
    }
    # dc3 {
    #   servers = 3
    #   clients = 3
    # }
  }


  # node_config {
  #   // Gateways are the last client in each DC.
  #   "dc1-client3" = {
  #     mesh_gateway = true
  #   }
  #   "dc2-client3" = {
  #     mesh_gateway = true
  #   }
  #   # "dc3-client3" = {
  #   #   mesh_gateway = true
  #   # }
  # }
}
