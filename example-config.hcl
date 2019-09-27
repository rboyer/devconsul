consul_image = "consul:1.6.1"

encryption {
  tls    = true
  gossip = true
}

kubernetes {
  enabled = false
}

topology {
  datacenters {
    dc1 {
      servers = 1
      clients = 2
    }
    dc2 {
      servers = 1
      clients = 2
    }
    dc3 {
      servers = 1
      clients = 2
    }
  }
}
