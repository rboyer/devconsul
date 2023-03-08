active = "simple-peering"

config "simple-peering" { # TODO finish
  consul_image = "consul-dev:latest"

  security {
    encryption {
      tls             = true
      gossip          = false
      server_tls_grpc = true
    }
    initial_master_token       = "root"
    disable_default_intentions = true
  }

  kubernetes {
    enabled = false
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
      mesh_gateways = 2
    }
    cluster "dc2" {
      servers = 1
      clients = 2
      # Gateways are the last client in each DC.
      mesh_gateways = 2
    }

    node "dc1-client1" {
      upstream_peer = "peer-dc2"
    }
    node "dc1-client2" {
      upstream_peer = "peer-dc2"
    }
    node "dc1-client3" { // MGW
      # partition = "default"
      # use_dns_wan_address = true
    }
    node "dc1-client4" { // MGW
      # partition = "default"
    }

    node "dc2-client1" {
      upstream_peer = "peer-dc1"
    }
    node "dc2-client2" {
      upstream_peer = "peer-dc1"
    }
    node "dc2-client3" { // MGW
      # partition = "default"
      # use_dns_wan_address = true
    }
    node "dc2-client4" { // MGW
      # partition = "default"
    }
  }

  cluster_config "dc1" {
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
      <<EOF
{
  "Kind": "exported-services",
  "Name": "default",
  "Services": [
    {
      "Name": "ping",
      "Consumers": [ { "Peer": "peer-dc2" } ]
    },
    {
      "Name": "pong",
      "Consumers": [ { "Peer": "peer-dc2" } ]
    }
  ]
}
  EOF
      ,
      <<EOF
{
  "Kind": "service-intentions",
  "Name": "ping",
  "Sources": [
    {
      "Name": "pong",
      "Peer": "peer-dc2",
      "Action": "allow"
    }
  ]
}
  EOF
      ,
      <<EOF
{
  "Kind": "service-intentions",
  "Name": "pong",
  "Sources": [
    {
      "Name": "ping",
      "Peer": "peer-dc2",
      "Action": "allow"
    }
  ]
}
  EOF
      ,
      <<EOF
{
  "Kind": "service-intentions",
  "Name": "*",
  "Sources": [
    {
      "Name": "*",
      "Action": "deny"
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
    "Kind": "proxy-defaults",
    "Name": "global",
    "Config": {
        "protocol": "http"
    }
  }
  EOF
      ,
      <<EOF
{
  "Kind": "exported-services",
  "Name": "default",
  "Services": [
    {
      "Name": "ping",
      "Consumers": [ { "Peer": "peer-dc1" } ]
    },
    {
      "Name": "pong",
      "Consumers": [ { "Peer": "peer-dc1" } ]
    }
  ]
}
  EOF
      ,
      <<EOF
{
  "Kind": "service-intentions",
  "Name": "ping",
  "Sources": [
    {
      "Name": "pong",
      "Peer": "peer-dc1",
      "Action": "allow"
    }
  ]
}
  EOF
      ,
      <<EOF
{
  "Kind": "service-intentions",
  "Name": "pong",
  "Sources": [
    {
      "Name": "ping",
      "Peer": "peer-dc1",
      "Action": "allow"
    }
  ]
}
EOF
      ,
      <<EOF
{
  "Kind": "service-intentions",
  "Name": "*",
  "Sources": [
    {
      "Name": "*",
      "Action": "deny"
    }
  ]
}
EOF
      ,
    ]
  }
}
