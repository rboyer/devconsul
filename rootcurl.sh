#!/bin/bash

set -euo pipefail

cd "$(dirname "$0")"

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
    echo "Missing required dc arg" >&2
    exit 1
fi
datacenter=$1
shift


node="${datacenter}-server1"
ip="$(./devconsul config | jq -r ".localAddrs[\"${node}\"]")"
if [[ "$ip" = "null" ]]; then
    echo "unknown dc: ${datacenter}" >&2
    exit 1
fi

if [[ $# -lt 1 ]]; then
    echo "missing required path portion" >&2
    exit 1
fi

path="$1"
shift

CONSUL_HTTP_TOKEN="$(master_token)"
if [[ -z "$CONSUL_HTTP_TOKEN" ]]; then
    exit 1
fi

exec curl -sL -H "x-consul-token: $CONSUL_HTTP_TOKEN" "http://${ip}:8500/${path}" "$@"
