#!/bin/sh

set -euo pipefail

case "${DP_CREDENTIAL_TYPE:-}" in
    static)
        # read from a token file
        readonly token_file="${SBOOT_TOKEN_FILE:-}"
        if [[ -z "${token_file}" ]]; then
            echo "missing required env var SBOOT_TOKEN_FILE" >&2
            exit 1
        fi
        if [[ ! -f "${token_file}" ]]; then
            echo "token file does not exist yet: ${token_file}" >&2
            exit 1
        fi

        token=""
        set +e
        read -r token < "${token_file}"
        set -e
        # trim any whitespace; this overdoes it in the middle, but tokens don't have
        # whitespace in the middle so :shrug:
        token="${token//[[:space:]]}"

        export DP_CREDENTIAL_STATIC_TOKEN="${token}"
        ;;
    *)
        ;;
esac

# if [[ -n "${DP_CA_CERTS:-}" ]]; then
#     mkdir -p /tmp/ca
#     cp "${DP_CA_CERTS}/consul-agent-ca.pem" /tmp/ca
#     export DP_CA_CERTS="/tmp/ca"
# fi

env | sort

exec consul-dataplane "$@"
