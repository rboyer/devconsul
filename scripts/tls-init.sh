#!/bin/bash

set -euo pipefail

# Remove undesirable side effects of CDPATH variable
unset CDPATH

cd "$(dirname "$(dirname "$0")")"

if ! command -v consul >/dev/null 2>&1 ; then
    echo "ERROR: no 'consul' binary on PATH. Please run 'make dev' from your consul checkout" >&2
    exit 1
fi

### extract relevant configuration ###

declare -A servers
declare -A clients
servers=()
clients=()

datacenters="$(./devconsul config | jq -r '.datacenters[]')"
for dc in $datacenters; do
    servers["$dc"]="$(./devconsul config topology.servers.${dc})"
    clients["$dc"]="$(./devconsul config topology.clients.${dc})"
done

### now do it ###

mkdir -p cache/tls
cd cache/tls

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

for dc in $datacenters; do
    num_servers="${servers[$dc]}"
    for id in $(seq 0 $((num_servers - 1))); do
        gen_server "$dc" "$id"
    done

    num_clients="${clients[$dc]}"
    for id in $(seq 0 $((num_clients - 1))); do
        gen_client "$dc" "$id"
    done
done
