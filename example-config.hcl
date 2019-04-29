consul_image = "consul:1.5.0"

encryption {
  tls    = true
  gossip = true
}

kubernetes {
  enabled = false
}

topology {
  servers {
    dc1 = 1
    dc2 = 1
  }

  clients {
    dc1 = 2
    dc2 = 2
  }
}
