provider "docker" {
  host = "unix:///var/run/docker.sock"
}

provider "nomad" {
  address = "http://127.0.0.1:4646"
  region  = "local"
}
