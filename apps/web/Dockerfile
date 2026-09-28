# syntax=docker/dockerfile:1
# Build context = repo root (pnpm workspace). See compose.yaml.

# ---- deps ----
FROM node:22-alpine AS deps
RUN corepack enable
WORKDIR /repo
COPY pnpm-workspace.yaml package.json pnpm-lock.yaml ./
COPY apps/web/package.json apps/web/
COPY packages/shared/package.json packages/shared/
COPY packages/ui/package.json packages/ui/
COPY packages/tsconfig/package.json packages/tsconfig/
RUN pnpm install --frozen-lockfile

# ---- build ----
FROM deps AS build
WORKDIR /repo
# NEXT_PUBLIC_* is inlined into the client bundle at build time, so it cannot
# be fixed up at runtime. Bake the local dev port in by default; CI overrides
# it with a build arg.
ARG NEXT_PUBLIC_API_URL=http://localhost:24080
ARG NEXT_PUBLIC_SSE_URL=http://localhost:24080/api/stream
ENV NEXT_PUBLIC_API_URL=${NEXT_PUBLIC_API_URL} \
    NEXT_PUBLIC_SSE_URL=${NEXT_PUBLIC_SSE_URL}
COPY . .
# Build workspace deps first, then the Next standalone output.
RUN pnpm --filter @smm/shared build \
 && pnpm --filter @smm/web build

# ---- runner ----
FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV PORT=3000
# Next standalone bundles the server + minimal node_modules.
COPY --from=build /repo/apps/web/.next/standalone ./
COPY --from=build /repo/apps/web/.next/static ./apps/web/.next/static
COPY --from=build /repo/apps/web/public ./apps/web/public
# distroless-style: run as the unprivileged node user.
USER node
EXPOSE 3000
# Image-level so both paths get it: compose services inherit it, and so does any
# container started from this image directly.
#
# 127.0.0.1, never localhost: busybox wget resolves localhost to ::1 first
# (/etc/hosts), and Next's standalone server listens on IPv4 only — so a
# `localhost` probe is a guaranteed ECONNREFUSED however healthy the app is.
# Anything 2xx/3xx is a served page; wget --spider exits non-zero on a
# connection failure or a 4xx/5xx, which is what a healthcheck wants.
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 \
  CMD wget -q --spider http://127.0.0.1:3000/login || exit 1
CMD ["node", "apps/web/server.js"]
