# syntax=docker/dockerfile:1
# Worker image: Node 22 + Chromium-only Playwright (headful) + Xvfb + x11vnc + noVNC.
# We install only Chromium (not the full Playwright bundle of Chromium+Firefox+WebKit)
# to keep the image ~1/3 the size while `--with-deps` still pulls the system libs.
# Build context = repo root.

FROM node:22-bookworm AS base
ENV DEBIAN_FRONTEND=noninteractive
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
ENTRYPOINT ["/usr/local/bin/worker-entrypoint.sh"]
