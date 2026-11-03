# Local SMM runtime

Running configuration: API/PostgreSQL/Redis/MinIO in Docker; Next.js dashboard on host port 24081.

Start backend without downloading build dependencies:

```sh
docker compose -f compose.yaml -f .local-runtime/compose.local.yaml up -d --no-build postgres redis minio minio-init migrate api
```

Start dashboard from repository root:

```sh
pnpm --filter @smm/web exec next dev -p 24081
```

API image smm-api:local contains the current cross-compiled Go binary, based on locally available smm-worker image. Dockerfile and binary are in this directory. Rebuild binary/image after API source changes. Worker image is the previously available smm-worker image; this startup did not rebuild it.

Verified 3 November 2026: /healthz OK; /readyz postgres/redis/minio up; dashboard /login HTTP 200; migration version 23 clean; existing 485 Instagram accounts; one worker healthy and reporting heartbeat. Runtime admission cap 5, reconciler interval 5s. No fleet load test or platform login performed.

Temporary build assets are excluded via .git/info/exclude.

## Recreate local API build files after a fresh checkout

The `.local-runtime` directory is machine-local and not committed. Normal startup
is `make up` when Docker registry access works. The fallback used here can be
recreated from the repository root after the `smm-worker` image is available:

```sh
mkdir -p .local-runtime
(cd apps/api && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ../../.local-runtime/server-linux ./cmd/server)
cat > .local-runtime/Dockerfile.api <<'DOCKERFILE'
FROM smm-worker:latest
COPY server-linux /app/server
ENTRYPOINT ["/app/server"]
DOCKERFILE
cat > .local-runtime/compose.local.yaml <<'COMPOSE'
services:
  api:
    image: smm-api:local
    build:
      context: ./.local-runtime
      dockerfile: Dockerfile.api
      target: null
    environment:
      RECONCILE_INTERVAL_SECONDS: 5
      ELASTIC_POOL_MAX_ACTIVE: 5
COMPOSE
docker build --pull=false -t smm-api:local -f .local-runtime/Dockerfile.api .local-runtime
```

This recipe targets Apple Silicon/Linux arm64. Keep `.local-runtime/` excluded
from Git; it holds build output and may hold large download caches. It does not
replace the standard production Dockerfile.
