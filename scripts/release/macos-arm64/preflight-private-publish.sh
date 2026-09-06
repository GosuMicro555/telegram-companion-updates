#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

main() {
  die "private builds cannot publish public updates; use publish-public-macos-arm64 for a confirmed public release"
}

main "$@"
