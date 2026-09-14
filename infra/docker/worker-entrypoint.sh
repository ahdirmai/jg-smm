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

# 1. Virtual framebuffer (the "headful" display).
Xvfb ":${DISPLAY_NUM}" -screen 0 "${SCREEN_GEOMETRY}" -nolisten tcp &
XVFB_PID=$!

# Wait for X to be ready.
for _ in $(seq 1 50); do
  if xdpyinfo -display ":${DISPLAY_NUM}" >/dev/null 2>&1; then break; fi
  sleep 0.1
done

# 2. VNC server bound to the X display (localhost only in compose).
x11vnc -display ":${DISPLAY_NUM}" -forever -shared -nopw -rfbport "${VNC_PORT}" -quiet &
X11VNC_PID=$!

# 3. noVNC web socket bridge.
websockify --web=/usr/share/novnc "${NOVNC_PORT}" "localhost:${VNC_PORT}" &
NOVNC_PID=$!

echo "{\"level\":\"info\",\"msg\":\"worker entrypoint ready\",\"workerId\":\"${WORKER_ID}\",\"display\":\"${DISPLAY}\",\"novncPort\":${NOVNC_PORT}}"

# 4. Run the worker (foreground; container lifetime == worker lifetime).
exec node /repo/apps/worker/dist/index.js
