#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

APP_PATH="${1:-}"
[[ "$APP_PATH" = /* ]] || die "live proxy smoke requires an absolute application path"
[[ -d "$APP_PATH" && ! -L "$APP_PATH" ]] || die "live proxy smoke application path is missing or unsafe"

for relative in \
  "Contents/Resources/tor/tor" \
  "Contents/Resources/tor/pluggable_transports/lyrebird" \
  "Contents/Resources/tor/pluggable_transports/pt_config.json"; do
  safe_regular_file_under_root "$APP_PATH" "$relative" >/dev/null
done

require_command go
require_command lsof
if lsof -nP -iTCP:19050 -sTCP:LISTEN >/dev/null 2>&1; then
  die "live proxy smoke requires loopback port 19050 to be free"
fi

resources="$APP_PATH/Contents/Resources"
(
  cd "$REPO_ROOT"
  TELEGRAM_COMPANION_LIVE_TOR=1 \
  TELEGRAM_COMPANION_LIVE_TOR_RESOURCES="$resources" \
    go test ./internal/proxy/tor \
      -run '^TestLiveManagedTorSnowflakeBootstrapAndShutdown$' -count=1
)

printf '%s\n' "live proxy smoke: PASS"
