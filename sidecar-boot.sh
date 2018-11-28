#!/bin/bash

set -euo pipefail

# -boot-token-file TOKEN AT_LEAST_ONE_CONSUL_ARG
if [[ $# -lt 3 ]]; then
    echo "usage: $0 -boot-token-file /path/to/token \$REST_OF_ARGS" >&2
    exit 1
fi

if [[ "$1" != '-boot-token-file' ]]; then
    echo "usage: $0 -boot-token-file /path/to/token \$REST_OF_ARGS" >&2
    exit 1
fi
shift

readonly token_file="$1"
shift

token=''
while : ; do
    read -r token < "${token_file}" || true
    if [[ -n "${token}" ]]; then
        break
    fi
    echo "waiting for secret to show up at ${token_file}..."
    sleep 0.1
done
# trim any whitespace; this overdoes it in the middle, but tokens don't have
# whitespace in the middle so :shrug:
token="${token//[[:space:]]}"

echo "Loaded token ${token} from ${token_file}"

exec consul connect envoy -token "${token}" "$@"
