#!/bin/sh

set -euo pipefail

if [[ ! -f consul-agent-ca-key.pem || ! -f consul-agent-ca.pem ]]; then
    consul tls ca create
fi

if [[ ! -f dc1-server-consul-0-key.pem || ! -f dc1-server-consul-0.pem ]]; then
    consul tls cert create -server
fi

if [[ ! -f dc1-client-consul-0-key.pem || ! -f dc1-client-consul-0.pem ]]; then
    consul tls cert create -client
fi

if [[ ! -f dc1-client-consul-1-key.pem || ! -f dc1-client-consul-1.pem ]]; then
    consul tls cert create -client
fi
