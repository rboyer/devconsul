#!/bin/bash

set -euo pipefail

readonly ready_file="${SBOOT_READY_FILE:-}"
readonly proxy_type="${SBOOT_PROXY_TYPE:-}"
readonly mode="${SBOOT_MODE:-}"
readonly agent_tls="${SBOOT_AGENT_TLS:-}"
readonly partition="${SBOOT_PARTITION:-}"

echo "launching a '${proxy_type}' sidecar proxy"

# wait until ready
while : ; do
    if [[ -f "${ready_file}" ]]; then
        break
    fi
    echo "waiting for system to be ready at ${ready_file}..."
    sleep 0.1
done

readonly service_register_file="${SBOOT_REGISTER_FILE:-}"
if [[ -z "${service_register_file}" ]]; then
    echo "missing required env var SBOOT_REGISTER_FILE" >&2
    exit 1
fi

api_args=()
case "${mode}" in
    insecure)
        ;;
    direct)
        readonly token_file="${SBOOT_TOKEN_FILE:-}"
        if [[ -z "${token_file}" ]]; then
            echo "missing required env var SBOOT_TOKEN_FILE" >&2
            exit 1
        fi

        api_args+=( -token-file "${token_file}" )

        ;;
    login)
        readonly bearer_token_file="${SBOOT_BEARER_TOKEN_FILE:-}"
        if [[ -z "${bearer_token_file}" ]]; then
            echo "missing required env var SBOOT_BEARER_TOKEN_FILE" >&2
            exit 1
        fi

        readonly token_sink_file="${SBOOT_TOKEN_SINK_FILE:-}"
        if [[ -z "${token_sink_file}" ]]; then
            echo "missing required env var SBOOT_TOKEN_SINK_FILE" >&2
            exit 1
        fi

        #TODO: handle api_args[@] here somehow
        consul login \
            -method=minikube \
            -bearer-token-file="${bearer_token_file}" \
            -token-sink-file="${token_sink_file}" \
            -meta "host=$(hostname)"

        echo "Wrote new token to ${token_sink_file}"

        api_args+=( -token-file "${token_sink_file}" )

        ;;
    *)
        echo "unknown mode: $mode" >&2
        exit 1
        ;;
esac

if [[ -n "${partition}" ]]; then
    api_args+=( -partition "${partition}" )
fi

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


if [[ "${mode}" != "insecure" ]]; then
    while : ; do
        if consul acl token read "${api_args[@]}" -self &> /dev/null ; then
            break
        fi

        echo "waiting for ACLs to work..."
        sleep 0.1
    done
fi

echo "Registering service..."
consul services register "${api_args[@]}" "${service_register_file}"

echo "Launching proxy..."
case "${proxy_type}" in
    envoy)
        consul connect envoy -bootstrap "${grpc_args[@]}" "${api_args[@]}" "$@" > /tmp/envoy.config
        exec consul connect envoy "${grpc_args[@]}" "${api_args[@]}" "$@"
        ;;
    builtin)
        # TODO: handle agent tls?
        exec consul connect proxy "${api_args[@]}" "$@"
        ;;
    *)
        echo "unknown proxy type: ${proxy_type}" >&2
        exit 1
esac
