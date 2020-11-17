#!/bin/bash

set -euo pipefail

unset CDPATH


echo "use the master token of: $(cat cache/master-token.val)"

set -x
exec google-chrome --incognito "http://10.0.1.11:8500/"
