#!/usr/bin/env bash
# Login wave for the xlsx-imported Instagram accounts.
#
# WHY A WAVE
# Instagram challenges logins that arrive in a burst. Firing 52 at once is the
# fastest way to get the whole slate rate-limited, so this dispatches N at a
# time and waits for them to settle before the next wave.
#
# WHY IT PRINTS noVNC URLs
# The login is HEADFUL. When Instagram shows a CAPTCHA or a 2FA wall there is no
# API to answer it — the operator has to click through it in the live browser.
# The worker can DETECT a 2FA code field (and then parks at NEEDS_INPUT), but it
# does NOT detect a CAPTCHA: it just waits out the 180s timeout and reports
# FAILED. So the URL has to be in front of the operator BEFORE that happens.
#
# SCOPE
# Only accounts on AUTO containers created by the xlsx import. The six MANUAL
# containers and their accounts are excluded on purpose.
set -euo pipefail

API="${API:-http://localhost:24080}"
WAVE="${WAVE:-5}"
SETTLE="${SETTLE:-90}"
COOKIES="${COOKIES:-/tmp/smm-cookies.txt}"

if [[ ! -s "$COOKIES" ]]; then
  echo "no session cookie at $COOKIES — log in first" >&2
  exit 1
fi

# Pending AUTO-container accounts only. Ordered so a partial run resumes where
# the last one stopped rather than restarting from the top.
#
# A temp file rather than `mapfile`: macOS ships bash 3.2, which predates it.
IDS_FILE=$(mktemp)
trap 'rm -f "$IDS_FILE"' EXIT
docker exec smm-postgres-1 psql -U smm -d smm -t -A -c "
  SELECT a.id FROM account a JOIN worker w ON w.id = a.worker_id
  WHERE w.source = 'AUTO' AND a.platform = 'INSTAGRAM'
    AND a.auth_status IN ('AUTHENTICATING','FAILED')
  ORDER BY a.username;" > "$IDS_FILE"

total=$(grep -c . "$IDS_FILE" || true)
if [[ "${total:-0}" -eq 0 ]]; then
  echo "nothing to log in — every AUTO account already settled"
  exit 0
fi

echo "dispatching $total account(s) in waves of $WAVE"
i=0
while read -r id; do
  [[ -z "$id" ]] && continue
  i=$((i+1))
  row=$(docker exec smm-postgres-1 psql -U smm -d smm -t -A -F'|' -c "
    SELECT a.username, w.novnc_service FROM account a JOIN worker w ON w.id = a.worker_id
    WHERE a.id = '$id';")
  user=${row%%|*}; url=${row##*|}

  code=$(curl -s -b "$COOKIES" -o /dev/null -w '%{http_code}' -X POST "$API/api/accounts/$id/login")
  case "$code" in
    202|200) echo "[$i] $user -> login dispatched";;
    *)       echo "[$i] $user -> dispatch FAILED (http $code)"; continue;;
  esac
  [[ -n "$url" ]] && echo "      live view: $url/vnc.html  (open now; CAPTCHA needs a human)"

  if (( i % WAVE == 0 )); then
    echo "--- wave complete, settling ${SETTLE}s"
    sleep "$SETTLE"
  fi
done < "$IDS_FILE"

echo
echo "== final status =="
docker exec smm-postgres-1 psql -U smm -d smm -c "
  SELECT a.auth_status, count(*)
  FROM account a JOIN worker w ON w.id = a.worker_id
  WHERE w.source = 'AUTO'
  GROUP BY 1 ORDER BY 2 DESC;"
