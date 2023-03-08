active = "wanfed-mgw"

config "wanfed-mgw" {
  consul_image = "consul-dev:latest"

  security {
    initial_master_token = "root"
    encryption {
      tls    = true
      gossip = true
    }
  }

  envoy {
    # log_level = "info"
    # log_level = "debug"
    log_level = "trace"
  }

  monitor {
    prometheus = false
  }

  kubernetes {
    enabled = false
  }

  config_entries = [
    <<EOF
  {
    "Kind": "proxy-defaults",
    "Name": "global",
    "MeshGateway": {
        "Mode":"local"
    },
    "Config": {
        "protocol": "http"
    }
  }
EOF
    ,
  ]

  topology {
    # network_shape = "flat"
    network_shape = "islands"


    cluster "dc1" {
      servers = 3
      clients = 8
      # Gateways are the last client in each DC.
      mesh_gateways = 1
    }
    cluster "dc2" {
      servers = 3
      clients = 2
      # Gateways are the last client in each DC.
      mesh_gateways = 1
    }

    node "dc1-client1" {
      upstream_name = "pong-dc2"

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
      upstream_datacenter = "dc2"

      service_meta = {
        version = "v2" // pong
      }
    }

    node "dc1-client5" {
      service_meta = {
        version = "v1" // ping
      }
    }

    node "dc1-client6" {
      service_meta = {
        version = "v1" // pong
      }
    }

    node "dc1-client7" {
      service_meta = {
        version = "v2" // ping
      }
    }

    node "dc1-client8" {
      service_meta = {
        version = "v2" // pong
      }
    }
  }
}
