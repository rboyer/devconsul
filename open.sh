#!/bin/bash

set -euo pipefail

unset CDPATH

readonly acls="$(devconsul config | jq -r ".acls")"
if [[ -n "$acls" ]]; then
    echo "use the master token of: $(cat cache/master-token.val)"
fi

set -x
exec google-chrome --incognito "http://10.0.1.11:8500/"
