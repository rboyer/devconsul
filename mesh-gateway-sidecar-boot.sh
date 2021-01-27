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

agent_tls=""
token_file=""
while getopts ":t:e" opt; do
    case "${opt}" in
        e)
            agent_tls=1
            ;;
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

api_args=()
grpc_args=()
if [[ -n "$agent_tls" ]]; then
    api_args+=(
        -ca-file /tls/consul-agent-ca.pem
        -http-addr https://127.0.0.1:8501
    )
    grpc_args+=( -grpc-addr https://127.0.0.1:8502 )
else
    api_args+=( -http-addr http://127.0.0.1:8500 )
    grpc_args+=( -grpc-addr http://127.0.0.1:8502 )
fi

echo "Launching mesh-gateway proxy..."
exec consul connect envoy \
    -register \
    -mesh-gateway \
    "${grpc_args[@]}" "${api_args[@]}" \
    -token-file "${token_file}" \
    "$@"
