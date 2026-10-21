# Android TURN transport

This private repository contains the Android source subset of
[`anton48/vk-turn-proxy-ios`](https://github.com/anton48/vk-turn-proxy-ios),
with Android network binding and the transport changes used by Vipin.
The upstream Git history is retained from commit
`5daeb9be9d3c7b0bcb4e01650f7c0b922070ab79` (see `UPSTREAM.txt`).
The iOS application and its build tooling are omitted from this branch.

The Android app pins this repository as `vendor/vk-turn-proxy` next to
`vendor/sing-box`. The core's Go module uses that sibling directory through
its existing `replace` directive. Build the native Android library from the
parent application's `scripts/build-core.sh`.

Run the quick transport tests with `go test ./pkg/...` from a Git checkout
(the upstream source-scan tests expect LF line endings). Run the full Go gate
with `bash tools/test.sh`; it includes race detection, slow permission-refresh
tests and the separate vendored speedtest suite.
The speedtest sources under `third_party/speedtest-go` remain vendored,
including their license and documented fork patch.

This derivative retains the upstream GPL-3.0 license and copyright notices.
