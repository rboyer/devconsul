storage "raft" {
  path    = "/vault/file"
  node_id = "vault1"
}

disable_mlock = true

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true
}

api_addr = "http://0.0.0.0:8200"
# cluster_addr = "https://127.0.0.1:8201"
ui = true
