# syntax=docker/dockerfile:1
# Worker image: Node 22 + Chromium-only Playwright (headful) + Xvfb + x11vnc + noVNC.
# We install only Chromium (not the full Playwright bundle of Chromium+Firefox+WebKit)
# to keep the image ~1/3 the size while `--with-deps` still pulls the system libs.
# Build context = repo root.

FROM node:22-bookworm AS base
ENV DEBIAN_FRONTEND=noninteractive
# This build host cannot fetch from deb.debian.org. Not a slow link — the name
# resolves to a single Akamai address (140.248.130.132) that refuses both 80 and
# 443, so every apt run dies with "Unable to connect to deb.debian.org". The
# same packages from the mirrors below downloaded 3/3 with no failures, so the
# sources are repointed with a fallback chain rather than a single substitute:
# apt walks the list per file and only the last entry is the unreachable one.
#
# Replaces the base image's sources entirely — it lists only deb.debian.org.
# Both a `bookworm` and a `bookworm-security` suite are needed; the security
# suite lives on a different root, so it cannot share the first entry's URIs.
RUN printf '%s\n' \
      'Types: deb' \
      'URIs: https://kartolo.sby.datautama.net.id/debian https://mirrors.ustc.edu.cn/debian https://deb.debian.org/debian' \
      'Suites: bookworm bookworm-updates' \
      'Components: main' \
      'Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg' \
      '' \
      'Types: deb' \
      'URIs: https://kartolo.sby.datautama.net.id/debian-security https://mirrors.ustc.edu.cn/debian-security https://deb.debian.org/debian-security' \
      'Suites: bookworm-security' \
      'Components: main' \
      'Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg' \
      > /etc/apt/sources.list.d/debian.sources \
 && printf 'Acquire::Retries "5";\n' > /etc/apt/apt.conf.d/99-retries
# Headful rendering under a virtual display, plus the noVNC live view.
RUN apt-get update && apt-get install -y --no-install-recommends \
      xvfb x11vnc novnc websockify \
 && rm -rf /var/lib/apt/lists/*

# ---- deps: pnpm install + download Chromium + its system libs ----
# Kept as one stage so the apt libs pulled by `playwright install --with-deps`
# stay in the final image.
FROM base AS deps
RUN corepack enable
WORKDIR /repo
COPY pnpm-workspace.yaml package.json pnpm-lock.yaml ./
COPY apps/worker/package.json apps/worker/
COPY packages/shared/package.json packages/shared/
COPY packages/tsconfig/package.json packages/tsconfig/
RUN pnpm install --frozen-lockfile

# Chromium-only, with system deps. PLAYWRIGHT_DOWNLOAD_HOST points at a mirror
# because the default azureedge CDN times out from some networks.
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
ARG PLAYWRIGHT_DOWNLOAD_HOST=https://cdn.npmmirror.com/binaries/playwright
ENV PLAYWRIGHT_DOWNLOAD_HOST=${PLAYWRIGHT_DOWNLOAD_HOST}
ENV PLAYWRIGHT_DOWNLOAD_CONNECTION_TIMEOUT=120000
RUN ./apps/worker/node_modules/.bin/playwright install --with-deps chromium \
 && rm -rf /root/.npm /var/lib/apt/lists/*

# ---- build: compile TS ----
FROM deps AS build
WORKDIR /repo
COPY . .
RUN pnpm --filter @smm/shared build \
 && pnpm --filter @smm/worker build

# ---- runtime ----
FROM deps AS runtime
WORKDIR /repo
ENV NODE_ENV=production
# Runtime config; identity/credentials are injected at run time, never baked in.
# WORKER_ID is intentionally NOT set here: the entrypoint derives it from the
# container hostname so `--scale worker=N` yields unique ids.
ENV API_URL=http://api:8080 \
    REDIS_URL=redis://redis:6379 \
    PROVISIONER_MODE=static \
    ACTION_DRY_RUN=true \
    DISPLAY=:99
# Built artifacts land next to the workspace node_modules so the @smm/shared
# symlink resolves at runtime.
COPY --from=build /repo/packages/shared/dist /repo/packages/shared/dist
COPY --from=build /repo/apps/worker/dist /repo/apps/worker/dist
COPY infra/docker/worker-entrypoint.sh /usr/local/bin/worker-entrypoint.sh
RUN chmod +x /usr/local/bin/worker-entrypoint.sh
# noVNC port (localhost-only mapping in compose) + worker liveness.
EXPOSE 6080
# Two independent things fail here, so probe both. `pgrep -x node` is liveness
# of the worker process itself (node is PID 1 after the entrypoint execs;
# heartbeat.ts calls process.exit after 3 failed heartbeats, so a dead worker is
# a real state, not a theoretical one). The noVNC fetch catches the other half —
# the display stack — which can die while node keeps running, and without which
# a login cannot be driven even though the worker is healthy enough to claim
# jobs.
#
# This lives in the image rather than in compose's service block because most
# workers are not compose services: the dashboard creates containers through the
# Docker Engine API (adapter/dockerprovisioner), which does not read compose at
# all. An image-level HEALTHCHECK is inherited by both paths; a compose one
# would cover only the `fleet` profile, leaving the containers operators
# actually run with no health at all.
#
# 127.0.0.1 rather than localhost: busybox wget resolves localhost to ::1
# first (/etc/hosts), so a v4-only listener refuses it. websockify happens to
# bind both today, but the probe should not depend on that.
#
# No curl in this image: busybox wget is the available HTTP client.
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=3 \
  CMD pgrep -x node >/dev/null && wget -q --spider http://127.0.0.1:6080/vnc.html
ENTRYPOINT ["/usr/local/bin/worker-entrypoint.sh"]
