#!/bin/bash

set -euo pipefail

ready_file="${1:-}"
shift

# wait until ready
while : ; do
    if [[ -f "${ready_file}" ]]; then
        break
    fi
    echo "waiting for system to be ready at ${ready_file}..."
    sleep 0.1
done

token_file=""
while getopts ":t:" opt; do
    case "${opt}" in
        t)
            token_file="$OPTARG"
            ;;
        \?)
            echo "invalid option: -$OPTARG" >&2
            exit 1
            ;;
        :)
            echo "invalid option: -$OPTARG requires an argument" >&2
            exit 1
            ;;
    esac
done
shift $((OPTIND - 1))

if [[ -z "${token_file}" ]]; then
    echo "missing required argument -t <BOOT_TOKEN_FILE>" >&2
    exit 1
fi

token=''
while : ; do
    read -r token < "${token_file}" || true
    if [[ -n "${token}" ]]; then
        break
    fi
    echo "waiting for secret to show up at ${token_file}..."
    sleep 0.1
done

echo "Launching mesh-gateway proxy..."
exec consul connect envoy \
    -register \
    -mesh-gateway \
    -token-file "${token_file}" \
    "$@"
