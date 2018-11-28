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

exec docker-compose exec -e CONSUL_HTTP_TOKEN="$(master_token)" dc1-server1 consul "$@"
