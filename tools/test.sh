#!/usr/bin/env bash
# Run the Go suites retained in the Android source subset.
set -uo pipefail
cd "$(dirname "$0")/.."

RACE=${RACE:-1}
fail=0
run() {
    printf '\n%s\n' "$1"
    shift
    if ! "$@"; then
        fail=1
    fi
}

goflags=(-count=1)
[[ "$RACE" == "1" ]] && goflags+=(-race)

# Include the permission-refresh tests in the full gate. Set VKTP_SLOW=
# explicitly to omit tests that wait for the TURN library's own timers.
export VKTP_SLOW=${VKTP_SLOW-1}

# The vendored fork's divergence guard compares against pristine upstream.
if ! go mod download github.com/showwin/speedtest-go@v1.7.11; then
    fail=1
fi

run "Android transport: root Go module" go test "${goflags[@]}" ./...
run "Vendored speedtest: separate Go module" \
    env -C third_party/speedtest-go go test "${goflags[@]}" ./speedtest/

exit "$fail"
