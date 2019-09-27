#!/bin/sh

set -euo pipefail

if [[ ! -f consul-agent-ca-key.pem || ! -f consul-agent-ca.pem ]]; then
    consul tls ca create
fi

gen_server() {
    local dc="$1"
    local id="$2"

    local prefix
    prefix="${dc}-server-consul-${id}"
    if [[ ! -f "${prefix}-key.pem" || ! -f "${prefix}.pem" ]]; then
        consul tls cert create -server -dc="${dc}"
    fi
}
gen_client() {
    local dc="$1"
    local id="$2"

    local prefix
    prefix="${dc}-client-consul-${id}"
    if [[ ! -f "${prefix}-key.pem" || ! -f "${prefix}.pem" ]]; then
        consul tls cert create -client -dc="${dc}"
    fi
}

for id in $(seq 0 $((N_SERVERS_DC1 - 1))); do
    gen_server dc1 "$id"
done

for id in $(seq 0 $((N_SERVERS_DC2 - 1))); do
    gen_server dc2 "$id"
done

for id in $(seq 0 $((N_SERVERS_DC3 - 1))); do
    gen_server dc3 "$id"
done

for id in $(seq 0 $((N_CLIENTS_DC1 - 1))); do
    gen_client dc1 "$id"
done

for id in $(seq 0 $((N_CLIENTS_DC2 - 1))); do
    gen_client dc2 "$id"
done

for id in $(seq 0 $((N_CLIENTS_DC3 - 1))); do
    gen_client dc3 "$id"
done
