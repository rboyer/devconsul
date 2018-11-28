# consul-cloud

This project helps bring up a local Consul Connect cluster using Docker.

## Prerequisites

* `go v1.11.4` or newer
* `docker`
* `docker-compose`
* `automake`
* `bash4`

## Getting Started

1. Run `make`. This will create any necessary docker containers that you may
   lack.
2. Run `make up`. This will bring up the containers with docker-compose, and
   then use `main.go` to bootstrap ACLs.
3. If you wish to destroy everything, run `make down`.

## Topology

Three "machines" are simulated in the manner of a Kubernetes Pod by
anchoring a network namespace to a single placeholder container (running
`google/pause:latest`) and then attaching any additional containers to it that
should be colocated
and share network things such as `127.0.0.1` and the `lo0` adapter.

This brings up a single consul cluster with 1 Server and 2 Client Agents
configured.  They are running on fixed IP addresses to make configuration
simple:

| Container                | IP        | Image              |
| ----------------         | --------- | ------------------ |
| dc1-server1-pod          | 10.0.1.11 | google/pause       |
| dc1-server1              | ^^^       | consul:1.4.0       |
| dc1-client1-pod          | 10.0.1.12 | google/pause       |
| dc1-client1              | ^^^       | consul:1.4.0       |
| dc1-client1-ping         | ^^^       | rboyer/pingpong    |
| dc1-client1-ping-sidecar | ^^^       | local/consul-envoy |
| dc1-client2-pod          | 10.0.1.13 | google/pause       |
| dc1-client2              | ^^^       | consul:1.4.0       |
| dc1-client2-pong         | ^^^       | rboyer/pingpong    |
| dc1-client2-pong-sidecar | ^^^       | local/consul-envoy |

The copies of pingpong running in the two pods are configured to dial each
other using Connect and exchange simple RPCs to showcase all of the plumbing in
action.
