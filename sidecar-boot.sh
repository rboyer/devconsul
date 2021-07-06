#!/bin/bash

set -euo pipefail

ready_file="${1:-}"
shift

proxy_type="${1:-}"
shift

echo "launching a '${proxy_type}' sidecar proxy"

mode="${1:-}"
shift

# wait until ready
while : ; do
    if [[ -f "${ready_file}" ]]; then
        break
    fi
    echo "waiting for system to be ready at ${ready_file}..."
    sleep 0.1
done

api_args=()
agent_tls=""
service_register_file=""
case "${mode}" in
    direct)
        token_file=""
        partition=""
        while getopts ":p:t:r:e" opt; do
            case "${opt}" in
                e)
                    agent_tls=1
                    ;;
                p)
                    partition="$OPTARG"
                    ;;
                t)
                    token_file="$OPTARG"
                    ;;
                r)
                    service_register_file="$OPTARG"
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
        if [[ -z "${service_register_file}" ]]; then
            echo "missing required argument -r <SERVICE_REGISTER_FILE>" >&2
            exit 1
        fi

        api_args+=( -token-file "${token_file}" )
        if [[ -n "${partition}" ]]; then
            api_args+=( -partition "${partition}" )
        fi

        ;;
    login)
        bearer_token_file=""
        token_sink_file=""
        partition=""
        while getopts ":p:t:s:r:e" opt; do
            case "${opt}" in
                e)
                    agent_tls=1
                    ;;
                t)
                    bearer_token_file="$OPTARG"
                    ;;
                p)
                    partition="$OPTARG"
                    ;;
                s)
                    token_sink_file="$OPTARG"
                    ;;
                r)
                    service_register_file="$OPTARG"
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

        if [[ -z "${bearer_token_file}" ]]; then
            echo "missing required argument -t <BEARER_TOKEN_FILE>" >&2
            exit 1
        fi
        if [[ -z "${token_sink_file}" ]]; then
            echo "missing required argument -s <TOKEN_SINK_FILE>" >&2
            exit 1
        fi
        if [[ -z "${service_register_file}" ]]; then
            echo "missing required argument -r <SERVICE_REGISTER_FILE>" >&2
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

while : ; do
    if consul acl token read "${api_args[@]}" -self &> /dev/null ; then
        break
    fi

    echo "waiting for ACLs to work..."
    sleep 0.1
done

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
