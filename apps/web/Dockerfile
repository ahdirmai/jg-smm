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
CMD ["node", "apps/web/server.js"]
