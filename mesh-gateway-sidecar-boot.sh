#!/bin/bash

set -euo pipefail

readonly mode="${SBOOT_MODE:-}"
readonly agent_tls="${SBOOT_AGENT_TLS:-}"
readonly agent_grpc_tls="${SBOOT_AGENT_GRPC_TLS:-}"
readonly partition="${SBOOT_PARTITION:-}"

api_args=()
acl_api_args=()
case "${mode}" in
    insecure)
        ;;
    direct)
        readonly token_file="${SBOOT_TOKEN_FILE:-}"
        if [[ -z "${token_file}" ]]; then
            echo "missing required env var SBOOT_TOKEN_FILE" >&2
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

        api_args+=( -token-file "${token_file}" )
        acl_api_args+=( -token-file "${token_file}" )

        ;;
    *)
        echo "unknown mode: $mode" >&2
        exit 1
        ;;
esac

if [[ -n "${partition}" ]]; then
    api_args+=( -partition "${partition}" )
    acl_api_args+=( -partition "${partition}" )
fi

if [[ -n "$agent_tls" ]]; then
    api_args+=(
        -ca-file /tls/consul-agent-ca.pem
        -http-addr https://127.0.0.1:8501
    )
else
    api_args+=( -http-addr http://127.0.0.1:8500 )
fi
acl_api_args+=( -http-addr http://127.0.0.1:8500 )

grpc_args=()
if [[ -n "$agent_grpc_tls" ]]; then
    grpc_args+=( -grpc-addr https://127.0.0.1:8503 )
else
    grpc_args+=( -grpc-addr http://127.0.0.1:8502 )
fi

if [[ "${mode}" != "insecure" ]]; then
    while : ; do
        if consul acl token read "${acl_api_args[@]}" -self ; then
            break
        fi

        echo "waiting for ACLs to work..."
        sleep 0.1
    done
fi

echo "Launching mesh-gateway proxy..."
exec consul connect envoy \
    -register \
    -mesh-gateway \
    "${grpc_args[@]}" "${api_args[@]}" \
    "$@"
