#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
probe_dir="$(mktemp -d "${TMPDIR:-/tmp}/domainry-runtime-timezone.XXXXXX")"
trap 'rm -rf "$probe_dir"' EXIT
chmod 0755 "$probe_dir"

cd "$project_root"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c \
  -o "$probe_dir/runtimehost.test" ./pkg/runtimehost
chmod 0555 "$probe_dir/runtimehost.test"

docker run --rm --platform linux/amd64 --network none --read-only \
  --cap-drop ALL --security-opt no-new-privileges --user 65534:65534 \
  --mount "type=bind,source=$probe_dir,target=/probe,readonly" \
  alpine:latest sh -ec '
    test ! -e /usr/share/zoneinfo/Asia/Tokyo
    test ! -d /usr/local/go
    exec /probe/runtimehost.test -test.run "^TestRuntimeHostApplicationTimeZones$" -test.v
  '
