#!/bin/bash

set -euo pipefail

unset CDPATH

cd "$(dirname "$0")"

single_file=""
if [[ $# -gt 0 ]]; then
    single_file="$1"
fi

if [[ -f config.hcl ]]; then
    # try to tear down prior run first
    devconsul down &>/dev/null || true
fi

rm -f config.hcl

cp -f ../Dockerfile-envoy .
cp -f ../versions.tf .
cp -f ../mesh-gateway-sidecar-boot.sh .
cp -f ../sidecar-boot.sh .

terraform init

failed=""
for fn in config.*.hcl; do
    if [[ -n "${single_file}" ]]; then
        if [[ "${fn}" != "${single_file}" ]]; then
            continue # skip
        fi
    fi
    cp -f $fn config.hcl
    echo "==== CASE: $fn ===="
    if [[ "config.wanfed-mgw.hcl" = "${fn}" ]]; then
        devconsul primary || {
            echo "FAIL: error bringing up primary environment: $fn" >&2
            failed="${failed},$fn(primary)"
        }
    fi
    devconsul up || {
        echo "FAIL: error bringing up rest of environment: $fn" >&2
        failed="${failed},$fn(up)"
    }
    devconsul down &>/dev/null || {
        echo "FAIL: error tearing down environment: $fn" >&2
        # failed="${failed},$fn(down)"
    }
done

rm -f config.hcl Dockerfile-envoy versions.tf mesh-gateway-sidecar-boot.sh sidecar-boot.sh

echo "========================="
if [[ -n "${failed}" ]]; then
    echo "OVERALL: FAILED" >&2
    echo "CASES: ${failed}" >&2
    exit 1
else
    echo "OVERALL: PASS"
    exit 0
fi
