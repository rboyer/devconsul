#!/bin/sh

# this is overloaded to also make gossip encryption

set -euo pipefail

if [[ ! -f gossip.key ]]; then
    consul keygen > gossip.key
fi

if [[ ! -f consul-agent-ca-key.pem || ! -f consul-agent-ca.pem ]]; then
    consul tls ca create
fi

if [[ ! -f dc1-server-consul-0-key.pem || ! -f dc1-server-consul-0.pem ]]; then
    consul tls cert create -server -dc=dc1
fi

if [[ ! -f dc2-server-consul-0-key.pem || ! -f dc2-server-consul-0.pem ]]; then
    consul tls cert create -server -dc=dc2
fi

if [[ ! -f dc1-client-consul-0-key.pem || ! -f dc1-client-consul-0.pem ]]; then
    consul tls cert create -client -dc=dc1
fi

if [[ ! -f dc1-client-consul-1-key.pem || ! -f dc1-client-consul-1.pem ]]; then
    consul tls cert create -client -dc=dc1
fi
