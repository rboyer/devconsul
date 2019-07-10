# devconsul

This project helps bring up a local Consul Connect cluster using Docker.

## Prerequisites

* `go v1.12.1` or newer
* `docker`
* `docker-compose`
* `automake`
* `bash4`

## Getting Started

1. Fork this repository. I'm not currently making guarantees about destructive
   changes to this repository.

2. Create a `consul.hcl` file (see below).

3. Run `make`. This will create any necessary docker containers that you may
   lack.

4. Run `make up`. This will bring up the containers with docker-compose, and
   then `devconsul boot` to bootstrap the cluster.

5. If you wish to destroy everything, run `make down`.

## If you are developing consul

1. From your `consul` working copy run `make dev-docker`. This will update a
   `consul-dev:latest` docker image locally.

2. From your `devconsul` working copy run `make force-docker` to rebuild the
   images locally to use that binary.

3. Set `consul_image = "consul-dev:latest"` in your `config.hcl` file
   (discussed below).  You may wish to just start from the
   `example-dev-connect--config.hcl` file in this repository.

## Configuration

There is a `config.hcl` file that should be of the form:

```hcl
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
```

## Topology

Two datacenters are configured using "machines" configured in the manner of a
Kubernetes pod by anchoring a network namespace to a single placeholder
container (running `google/pause:latest`) and then attaching any additional
containers to it that should be colocated and share network things such as
`127.0.0.1` and the `lo0` adapter.

An example using a topology of `servers { dc1=1 dc2=1 } clients { dc1=2
dc2=2}`:

| Container                | IP        | Image              |
| ----------------         | --------- | ------------------ |
| dc1-server1-pod          | 10.0.1.11 | google/pause       |
| dc1-server1              | ^^^       | consul:1.5.0       |
| dc1-client1-pod          | 10.0.1.12 | google/pause       |
| dc1-client1              | ^^^       | consul:1.5.0       |
| dc1-client1-ping         | ^^^       | rboyer/pingpong    |
| dc1-client1-ping-sidecar | ^^^       | local/consul-envoy |
| dc1-client2-pod          | 10.0.1.13 | google/pause       |
| dc1-client2              | ^^^       | consul:1.5.0       |
| dc1-client2-pong         | ^^^       | rboyer/pingpong    |
| dc1-client2-pong-sidecar | ^^^       | local/consul-envoy |
| dc2-server1-pod          | 10.0.2.11 | google/pause       |
| dc2-server1              | ^^^       | consul:1.5.0       |
| dc2-client1-pod          | 10.0.2.12 | google/pause       |
| dc2-client1              | ^^^       | consul:1.5.0       |
| dc2-client1-ping         | ^^^       | rboyer/pingpong    |
| dc2-client1-ping-sidecar | ^^^       | local/consul-envoy |
| dc2-client2-pod          | 10.0.2.13 | google/pause       |
| dc2-client2              | ^^^       | consul:1.5.0       |
| dc2-client2-pong         | ^^^       | rboyer/pingpong    |
| dc2-client2-pong-sidecar | ^^^       | local/consul-envoy |

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
