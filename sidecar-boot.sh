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

case "${mode}" in
    direct)
        token_file=""
        service_register_file=""
        while getopts ":t:r:" opt; do
            case "${opt}" in
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

        while : ; do
            if consul acl token read -token-file "${token_file}" -self &> /dev/null ; then
                break
            fi

            echo "waiting for ACLs to work..."
            sleep 0.1
        done

        echo "Registering service..."
        consul services register -token-file "${token_file}" "${service_register_file}"

        echo "Launching proxy..."
        case "${proxy_type}" in
            envoy)
                consul connect envoy -bootstrap -token-file "${token_file}" "$@" > /tmp/envoy.config
                exec consul connect envoy -token-file "${token_file}" "$@"
                ;;
            builtin)
                exec consul connect proxy -token-file "${token_file}" "$@"
                ;;
            *)
                echo "unknown proxy type: ${proxy_type}" >&2
                exit 1
        esac
        ;;
    login)
        bearer_token_file=""
        token_sink_file=""
        service_register_file=""
        while getopts ":t:s:r:" opt; do
            case "${opt}" in
                t)
                    bearer_token_file="$OPTARG"
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

        consul login \
            -method=minikube \
            -bearer-token-file="${bearer_token_file}" \
            -token-sink-file="${token_sink_file}" \
            -meta "host=$(hostname)"

        echo "Wrote new token to ${token_sink_file}"

        echo "Registering service..."
        consul services register -token-file "${token_sink_file}" "${service_register_file}"

        echo "Launching proxy..."
        case "${proxy_type}" in
            envoy)
                consul connect envoy -bootstrap -token-file "${token_sink_file}" "$@" > /tmp/envoy.config
                exec consul connect envoy -token-file "${token_sink_file}" "$@"
                ;;
            builtin)
                exec consul connect proxy -token-file "${token_sink_file}" "$@"
                ;;
            *)
                echo "unknown proxy type: ${proxy_type}" >&2
                exit 1
        esac
        ;;
    *)
        echo "unknown mode: $mode" >&2
        exit 1
        ;;
esac

