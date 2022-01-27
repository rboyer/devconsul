# devconsul

This project helps bring up a local Consul Connect cluster using Docker.

## Prerequisites

* `go v1.15.2` or newer
* `docker`
* `terraform`
* `automake`
* `bash4`

### If Kubernetes is Enabled

* `kubectl`
* `minikube`

## Getting Started

1. Fork this repository. I'm not currently making guarantees about destructive
   changes to this repository.

2. Install the `devconsul` binary with `make install` (or just `go install`).

3. Create a `consul.hcl` file (see below).

4. Run `devconsul up`. This will do all of the interesting things.

5. If you wish to destroy everything, run `devconsul down`.

## If you are developing consul

1. From your `consul` working copy run `make dev-docker`. This will update a
   `consul-dev:latest` docker image locally.

2. From your `devconsul` working copy run `devconsul docker` to rebuild the
   images locally to use that binary.

3. Set `consul_image = "consul-dev:latest"` in your `config.hcl` file
   (discussed below).  You may wish to just start from the
   `example-config.hcl` file in this repository using the `simple` configuration.

## Configuration

There is a `config.hcl` file that should be of the form:

```hcl
active = "default" # picks which 'config "<name>" { ... }' block to use

# you can have multiple config blocks and toggle between them
config "default" {
  consul_image = "consul:1.5.0"

  security {
    encryption {
      tls    = true
      gossip = true
    }
  }

  kubernetes {
    enabled = false
  }

  topology {
    network_shape = "flat"
    datacenter "dc1" {
      servers = 1
      clients = 2
    }
    datacenter "dc2" {
      servers = 1
      clients = 2
    }
  }
}
```

## Topology

By default, two datacenters are configured using "machines" configured in the
manner of a Kubernetes pod by anchoring a network namespace to a single
placeholder container (running `k8s.grc.io/pause:3.3`) and then attaching any
additional containers to it that should be colocated and share network things
such as `127.0.0.1` and the `lo0` adapter.

An example using a topology of `servers { dc1=1 dc2=1 } clients { dc1=2
dc2=2}`:

| Container                | IP        | Image                |
| ----------------         | --------- | -------------------- |
| dc1-server1-pod          | 10.0.1.11 | k8s.grc.io/pause:3.3 |
| dc1-server1              | ^^^       | consul:1.5.0         |
| dc1-client1-pod          | 10.0.1.12 | k8s.grc.io/pause:3.3 |
| dc1-client1              | ^^^       | consul:1.5.0         |
| dc1-client1-ping         | ^^^       | rboyer/pingpong      |
| dc1-client1-ping-sidecar | ^^^       | local/consul-envoy   |
| dc1-client2-pod          | 10.0.1.13 | k8s.grc.io/pause:3.3 |
| dc1-client2              | ^^^       | consul:1.5.0         |
| dc1-client2-pong         | ^^^       | rboyer/pingpong      |
| dc1-client2-pong-sidecar | ^^^       | local/consul-envoy   |
| dc2-server1-pod          | 10.0.2.11 | k8s.grc.io/pause:3.3 |
| dc2-server1              | ^^^       | consul:1.5.0         |
| dc2-client1-pod          | 10.0.2.12 | k8s.grc.io/pause:3.3 |
| dc2-client1              | ^^^       | consul:1.5.0         |
| dc2-client1-ping         | ^^^       | rboyer/pingpong      |
| dc2-client1-ping-sidecar | ^^^       | local/consul-envoy   |
| dc2-client2-pod          | 10.0.2.13 | k8s.grc.io/pause:3.3 |
| dc2-client2              | ^^^       | consul:1.5.0         |
| dc2-client2-pong         | ^^^       | rboyer/pingpong      |
| dc2-client2-pong-sidecar | ^^^       | local/consul-envoy   |

The copies of pingpong running in the two pods are configured to dial each
other using Connect and exchange simple RPCs to showcase all of the plumbing in
action.

## Warning about running on OSX

Everything works fine on a linux machine as long as docker is running directly
on your host machine where you are running the makefile and helper scripts.

For some reasons Docker-for-mac does not make networks created with
docker/docker-compose routable from the host laptop. The networks work
perfectly well _inside of the VM_ but not from the host.

This is troublesome because this repository assumes that the place the
makefile/scripts/go-program run from can access the `10.0.0.0/16` address space
and have it be routed to the correct container.

There are three awkward solutions:

1. Actually run this repository from within the VM.

2. Use some CLI route table commands to intercept `10.0.0.0/16` and route it to
   the VM.

3. Use linux.
