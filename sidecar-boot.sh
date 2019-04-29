#!/bin/bash

set -euo pipefail

mode="${1:-}"
shift

case "${mode}" in
    direct)
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
        # trim any whitespace; this overdoes it in the middle, but tokens don't have
        # whitespace in the middle so :shrug:
        token="${token//[[:space:]]}"

        echo "Loaded token ${token} from ${token_file}"

        exec consul connect envoy -token "${token}" "$@"
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

        consul connect envoy -bootstrap -token-file "${token_sink_file}" "$@" > /tmp/envoy.config
        exec consul connect envoy -token-file "${token_sink_file}" "$@"
        ;;
    *)
        echo "unknown mode: $mode" >&2
        exit 1
        ;;
esac

