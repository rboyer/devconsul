active = "tls-api"

config "tls-api" {
  consul_image  = "consul-dev:latest"
  envoy_version = "v1.22.5"

  security {
    initial_master_token = "root"

    encryption {
      tls     = true
      tls_api = true
      gossip  = true
    }
  }

  envoy {
    log_level = "debug"
  }

  monitor {
    prometheus = false
  }

  kubernetes {
    enabled = false
  }

  # "MeshGateway": {
  #     "Mode":"local"
  # },
  config_entries = [
    <<EOF
  {
    "Kind": "proxy-defaults",
    "Name": "global",
    "Config": {
        "protocol": "http"
    }
  }
  EOF
    ,
  ]

  topology {
    network_shape = "flat"

    # network_shape = "islands"

    cluster "dc1" {
      servers = 3
      clients = 8
    }
    cluster "dc2" {
      servers = 3
      clients = 2
    }

    node "dc1-client1" {
      upstream_name = "pong-dc2"

      # canary = true

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
