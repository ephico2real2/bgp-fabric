# shellcheck shell=bash
# browser-shot.sh — headless Chrome screenshot with a file-wait and a kill.
# Source this file, then call browser_shot.
#
# Required env: CHROME
# Per-shot env, set by the caller before each call:
#   BROWSER_SHOT_PATH   where the PNG goes (absolute)
#   BROWSER_SHOT_URL    what to photograph
#   BROWSER_SHOT_WIDTH / BROWSER_SHOT_HEIGHT
#   BROWSER_SHOT_VIRTUAL_TIME_MS  how long Chrome's clock runs before the shot
#   BROWSER_SHOT_FILE_LABEL       the path printed by `file` in the transcript
# Optional: BROWSER_SHOT_RESOLVER_RULES (a MAP rule for a name with no DNS).
browser_shot() {
  if [ ! -x "$CHROME" ]; then
    echo "Chrome is absent at $CHROME — skipping screenshot"
    return 0
  fi
  local shot="${BROWSER_SHOT_PATH:-$PWD/$HERE/output/browser.png}"
  local url="${BROWSER_SHOT_URL:-http://${HTTP_HOST}/orders}"
  local width="${BROWSER_SHOT_WIDTH:-1000}"
  local height="${BROWSER_SHOT_HEIGHT:-500}"
  local file_label="${BROWSER_SHOT_FILE_LABEL:-$HERE/output/browser.png}"
  local profile rc=0 pid i waited="" size1 size2
  # extra: what Chrome is given. shown: what is echoed into the transcript,
  # with the resolver rule quoted as a reader would have to type it.
  local -a extra=() shown=()
  # Measured 2026-09-19: Chrome 153 writes the PNG (1.40 s) and then hangs in a
  # network-service crash loop instead of exiting (gotcha #121). The PNG on disk is
  # the result, so the wait is FOR THE FILE — polled every 0.2 s, 60 s ceiling — and
  # Chrome is killed the moment the file exists (rc 143 expected). A throwaway
  # profile, --no-first-run. Never claim a write that did not happen.
  profile=$(mktemp -d)
  rm -f "$shot"
  if [ -n "${BROWSER_SHOT_RESOLVER_RULES:-}" ]; then
    extra+=(--host-resolver-rules="$BROWSER_SHOT_RESOLVER_RULES")
    shown+=("--host-resolver-rules=\"$BROWSER_SHOT_RESOLVER_RULES\"")
  elif [ -n "${HTTP_HOST:-}" ] && [ -n "${HTTP_ADDR:-}" ]; then
    extra+=(--host-resolver-rules="MAP ${HTTP_HOST} ${HTTP_ADDR}")
    shown+=("--host-resolver-rules=\"MAP ${HTTP_HOST} ${HTTP_ADDR}\"")
  fi
  if [ -n "${BROWSER_SHOT_VIRTUAL_TIME_MS:-}" ]; then
    extra+=(--virtual-time-budget="$BROWSER_SHOT_VIRTUAL_TIME_MS")
    shown+=("--virtual-time-budget=$BROWSER_SHOT_VIRTUAL_TIME_MS")
  fi
  echo "$CHROME --headless=new --disable-gpu --no-first-run --window-size=${width},${height} --user-data-dir=<tmp>${shown[*]:+ ${shown[*]}} --screenshot=$shot $url"
  "$CHROME" --headless=new --disable-gpu --no-first-run --window-size="${width},${height}" \
    --user-data-dir="$profile" \
    "${extra[@]}" \
    --screenshot="$shot" \
    "$url" >/dev/null 2>&1 &
  pid=$!
  for i in $(seq 1 300); do
    # a non-empty file is not a finished file: require the size to hold for one
    # more poll before Chrome is killed, or a half-written PNG would pass `-s`
    if [ -s "$shot" ]; then
      size1=$(stat -f %z "$shot" 2>/dev/null || stat -c %s "$shot")
      sleep 0.2
      size2=$(stat -f %z "$shot" 2>/dev/null || stat -c %s "$shot")
      if [ "$size1" = "$size2" ]; then
        waited=$(awk -v n="$i" 'BEGIN{printf "%.1f", (n+1)*0.2}')
        break
      fi
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      break
    fi
    sleep 0.2
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
  fi
  wait "$pid" 2>/dev/null || rc=$?
  rm -rf "$profile"
  if [ -s "$shot" ]; then
    echo "screenshot written after ${waited:-<0.2} s; chrome_rc=$rc"
    file "$file_label"
  else
    # recorded, not fatal: apply records what happened, check.sh judges it
    echo "no screenshot written within 60 s"
  fi
}
