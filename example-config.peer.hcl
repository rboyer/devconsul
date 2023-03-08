# active = "simple"
# active = "simple-no-acls"
active = "simple-peering"

config "simple-peering" {
  consul_image = "consul-dev:latest"

  envoy_version = "v1.22.0"

  security {
    disable_acls = true
    encryption {
      tls    = false
      gossip = false
    }
  }

  kubernetes {
    enabled = false
  }

  enterprise {
    # enabled = false
    enabled      = true
    license_path = "/home/rboyer/.consul.dev.licence"
  }

  envoy {
    log_level = "trace"
    # log_level = "info"
    # log_level = "debug"
  }

  topology {
    link_mode = "peer"
    cluster "dc1" {
      servers = 1
      clients = 2
      # Gateways are the last client in each DC.
      # mesh_gateways = 1
    }
    cluster "dc2" {
      servers = 1
      clients = 2
      # Gateways are the last client in each DC.
      # mesh_gateways = 1
    }

    node "dc1-client1" {
      upstream_peer = "peer-dc2"
    }
    node "dc1-client2" {
      upstream_peer = "peer-dc2"
    }
    node "dc2-client1" {
      upstream_peer = "peer-dc1"
    }
    node "dc2-client2" {
      upstream_peer = "peer-dc1"
    }
  }

  cluster_config "dc1" {
    config_entries = [
      <<EOF
{
  "Kind": "exported-services",
  "Name": "default",
  "Services": [
    {
      "Name": "ping",
      "Consumers": [ { "PeerName": "peer-dc2" } ]
    },
    {
      "Name": "pong",
      "Consumers": [ { "PeerName": "peer-dc2" } ]
    }
  ]
}
  EOF
      ,
    ]
  }

  cluster_config "dc2" {
    config_entries = [
      <<EOF
{
  "Kind": "exported-services",
  "Name": "default",
  "Services": [
    {
      "Name": "ping",
      "Consumers": [ { "PeerName": "peer-dc1" } ]
    },
    {
      "Name": "pong",
      "Consumers": [ { "PeerName": "peer-dc1" } ]
    }
  ]
}
  EOF
      ,
    ]
  }
}

config "simple-no-acls" {
  consul_image = "consul-dev:latest"

  envoy_version = "v1.22.0"

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

  enterprise {
    enabled      = true
    license_path = "/home/rboyer/.consul.dev.licence"
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

config "simple" {
  consul_image = "consul-dev:latest"

  envoy_version = "v1.22.0"

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

  enterprise {
    enabled      = true
    license_path = "/home/rboyer/.consul.dev.licence"
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

