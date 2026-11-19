#!/bin/sh
# Render the per-machine worker env from the sealed base file. boxd gives every
# machine its own hostname, and the worker's ticket identity is that hostname.
set -eu
name=$(hostname)
base=/home/boxd/smm/.boxenv.base
out=/home/boxd/smm/.boxenv
umask 077
grep -vE '^(WORKER_ID|NOVNC_URL)=' "$base" > "$out"
printf 'WORKER_ID=%s\nNOVNC_URL=https://%s.boxd.sh/novnc\n' "$name" "$name" >> "$out"
chmod 600 "$out"
