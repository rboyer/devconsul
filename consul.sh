#!/bin/bash

set -euo pipefail

cd "$(dirname "$0")"

if ! command -v consul >/dev/null 2>&1 ; then
    echo "ERROR: no 'consul' binary on PATH. Please run 'make dev' from your consul checkout" >&2
    exit 1
fi

readonly master_token_file=./cache/master-token.val

master_token() {
    if [[ ! -f "${master_token_file}" ]]; then
        echo "no master token defined in ${master_token_file}" >&2
        exit 1
    fi

    local token
    read -r token < "${master_token_file}"

    # trim any whitespace; this overdoes it in the middle, but tokens don't have
    # whitespace in the middle so :shrug:
    echo "${token//[[:space:]]}"
}

if [[ $# -lt 1 ]]; then
    echo "Missing required cluster arg" >&2
    exit 1
fi
cluster=$1
shift

node="${cluster}-server1"
error_message="unknown cluster: ${cluster}"
if [[ "$cluster" == *-* ]]; then
    # actually this is a node query
    node="${cluster}"
    error_message="unknown node: ${node}"
fi

ip="$(devconsul config | jq -r ".localAddrs[\"${node}\"]")"
if [[ "$ip" = "null" ]]; then
    echo "${error_message}" >&2
    exit 1
fi

export CONSUL_HTTP_ADDR="http://${ip}:8500"

readonly acls="$(devconsul config | jq -r ".acls")"
if [[ -n "$acls" ]]; then
    export CONSUL_HTTP_TOKEN="$(master_token)"
    if [[ -z "$CONSUL_HTTP_TOKEN" ]]; then
        exit 1
    fi
fi

exec consul "$@"
