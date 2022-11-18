#!/bin/bash

set -euo pipefail

cd "$(dirname "$0")"

if ! command -v vault >/dev/null 2>&1 ; then
    echo "ERROR: no 'vault' binary on PATH." >&2
    exit 1
fi

readonly root_token_file=./cache/vault-token.val

vault_root_token() {
    if [[ ! -f "${root_token_file}" ]]; then
        echo "no vault root token defined in ${root_token_file}" >&2
        exit 1
    fi

    local token
    read -r token < "${root_token_file}"

    # trim any whitespace; this overdoes it in the middle, but tokens don't have
    # whitespace in the middle so :shrug:
    echo "${token//[[:space:]]}"
}

export VAULT_ADDR="http://10.0.100.111:8200"
export VAULT_TOKEN="$(vault_root_token)"
if [[ -z "$VAULT_TOKEN" ]]; then
    exit 1
fi

exec vault "$@"
