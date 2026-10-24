#!/usr/bin/env bash
# Fails if statement coverage of either module is below MIN (default 90).
# Example programs (examples/) are excluded: they are documentation, run by
# hand and in the integration tests.
#
#   hack/coverage.sh          # writes coverage.out and receiver-coverage.out
#   MIN=95 hack/coverage.sh
set -euo pipefail
MIN=${MIN:-90}
cd "$(dirname "$0")/.."

# total prints a profile's statement coverage; run it inside the profile's
# module so go tool cover can find the sources.
total() {
    (cd "$1" && go tool cover -func="$2" | awk '/^total:/ {sub("%", "", $3); print $3}')
}

go test -count=1 -coverprofile=coverage.raw ./... >/dev/null
grep -v '/examples/' coverage.raw >coverage.out
rm coverage.raw
root=$(total . "$PWD/coverage.out")

(cd receiver/systemdreportreceiver && go test -count=1 -coverprofile=../../receiver-coverage.out ./... >/dev/null)
receiver=$(total receiver/systemdreportreceiver "$PWD/receiver-coverage.out")

printf 'coverage: sdmetrics %s%%, systemdreportreceiver %s%% (minimum %s%%)\n' "$root" "$receiver" "$MIN"
fail=0
for v in "$root" "$receiver"; do
    if awk -v v="$v" -v m="$MIN" 'BEGIN {exit !(v < m)}'; then fail=1; fi
done
if [ "$fail" = 1 ]; then
    echo "coverage below ${MIN}%" >&2
    go tool cover -func=coverage.out | sort -k3 -n | head -15 >&2
    exit 1
fi
