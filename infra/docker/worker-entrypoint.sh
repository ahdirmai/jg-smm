#!/usr/bin/env bash
# Worker container entrypoint: start a virtual display + VNC live view for
# headful login, then run the worker process. Identity comes from env.
set -euo pipefail

DISPLAY_NUM="${DISPLAY_NUM:-99}"
SCREEN_GEOMETRY="${SCREEN_GEOMETRY:-1280x800x24}"
NOVNC_PORT="${NOVNC_PORT:-6080}"
VNC_PORT="${VNC_PORT:-5900}"

export DISPLAY=":${DISPLAY_NUM}"

# Derive a stable, unique worker identity from the container hostname so that
# `docker compose --scale worker=N` yields worker-<hostname> without collisions.
export WORKER_ID="${WORKER_ID:-worker-$(hostname)}"
export CONTROL_CHANNEL="${CONTROL_CHANNEL:-control-${WORKER_ID}}"
export ACTION_QUEUE="${ACTION_QUEUE:-queue:action:${WORKER_ID}}"

cleanup() {
  # Best-effort teardown; ignore missing PIDs.
  [[ -n "${XVFB_PID:-}" ]] && kill "${XVFB_PID}" 2>/dev/null || true
  [[ -n "${X11VNC_PID:-}" ]] && kill "${X11VNC_PID}" 2>/dev/null || true
  [[ -n "${NOVNC_PID:-}" ]] && kill "${NOVNC_PID}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# 0. Clear a stale display socket left by a PREVIOUS boot.
#
# A container that was stopped and started again (the PAUSED → RUNNING path)
# finds /tmp/.X11-unix/X99 still on the container's writable layer, owned by a
# dead Xvfb. The readiness loop below then passes on the FIRST iteration —
# the file exists — and Chromium launches against a display nothing is serving.
# The failure surfaces far away, as "Missing X server or $DISPLAY" inside a
# login, which reads as a Playwright bug rather than a boot race.
# Both artifacts, not just the socket: Xvfb refuses to start ("Server is
# already active for display 99") while .X<num>-lock exists, and deleting only
# the socket turns that into a restart loop — the container exits 1, Docker
# restarts it, and it finds the lock again.
rm -f "/tmp/.X11-unix/X${DISPLAY_NUM}" "/tmp/.X${DISPLAY_NUM}-lock"

# 1. Virtual framebuffer (the "headful" display).
Xvfb ":${DISPLAY_NUM}" -screen 0 "${SCREEN_GEOMETRY}" -nolisten tcp &
XVFB_PID=$!

# Wait for X to be ready.
#
# Gate on the display socket, not on xdpyinfo: x11-utils is not installed in
# this image (only xvfb/x11vnc/novnc/websockify), so the old `xdpyinfo` probe
# exited 127 on every iteration and this loop always ran its full 50 × 0.1s —
# a flat 5s added to every worker boot, including every reconciler-driven
# recreate. The socket is what x11vnc and Chromium actually connect to, and it
# appears once Xvfb is serving; the two-iteration confirmation avoids racing
# the socket file's creation.
seen=0
for _ in $(seq 1 50); do
  if [ -e "/tmp/.X11-unix/X${DISPLAY_NUM}" ]; then
    seen=$((seen + 1))
    [ "${seen}" -ge 2 ] && break
  else
    seen=0
  fi
  sleep 0.1
done
if [ "${seen}" -lt 2 ]; then
  echo "{\"level\":\"error\",\"msg\":\"X display never became ready\",\"display\":\"${DISPLAY}\"}" >&2
  exit 1
fi

# 2. VNC server bound to the X display (localhost only in compose).
x11vnc -display ":${DISPLAY_NUM}" -forever -shared -nopw -rfbport "${VNC_PORT}" -quiet &
X11VNC_PID=$!

# 3. noVNC web socket bridge.
websockify --web=/usr/share/novnc "${NOVNC_PORT}" "localhost:${VNC_PORT}" &
NOVNC_PID=$!

echo "{\"level\":\"info\",\"msg\":\"worker entrypoint ready\",\"workerId\":\"${WORKER_ID}\",\"display\":\"${DISPLAY}\",\"novncPort\":${NOVNC_PORT}}"

# 4. Run the worker (foreground; container lifetime == worker lifetime).
exec node /repo/apps/worker/dist/index.js
