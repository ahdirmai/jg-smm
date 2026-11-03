# Deploy — Kubernetes (production tier)

> **Audience:** an agent or engineer with SSH + sudo on the target host. Commands
> are ordered; each phase ends with a **Gate** that must pass before the next.
> Manifests live in [`infra/k8s/`](../infra/k8s/); this file is the install
> order, the config values, and the failure modes.
>
> **Why this exists:** local dev is `docker compose` by design
> ([INFRA_ANALYST](INFRA_ANALYST.md) §15.1 keeps K8s off the Mac). Production
> runs the **worker fleet as pods**, provisioned by the API's `k8s` driver.

## 0. Constraints that shape this deploy

1. **The API must run inside the cluster.** `k8s.New` uses
   `rest.InClusterConfig()` (`apps/api/internal/adapter/k8s/client.go`). Running
   it on the host with a kubeconfig fails at startup.
2. **The worker pods are bare Pods created by the API**, not Deployment
   replicas. `ensurePod` sets image, limits, PVC and `/dev/shm` only — **no env**
   (see gap 10.2). Their defaults (`API_URL=http://api:8080`,
   `REDIS_URL=redis://redis:6379`, `ACTION_DRY_RUN=true`) therefore depend on
   the Service names `api`/`redis`/`minio` in namespace `smm`.
3. **Live view (noVNC) has no K8s path.** The dashboard iframe loads
   `novncUrl` from the operator's browser
   (`apps/web/components/live-browser-modal.tsx`), while INFRA_ANALYST §7 keeps
   noVNC `ClusterIP`-only. No ingress is published for it here (gap 10.1).
   Logins that need 2FA are not completable from the dashboard in this tier
   until that is settled.

## 1. Target shape

| Component | Workload | Replicas | Pool (when >1 node) |
| --- | --- | --- | --- |
| API | Deployment | 3 | `smm-app` |
| Web | Deployment | 2 | `smm-app` |
| Worker | Pod per `Worker` row | dynamic | `smm-worker` |
| Postgres + Timescale | StatefulSet | 1 | `smm-data` |
| Redis | StatefulSet | 1 | `smm-data` |
| MinIO | StatefulSet | 1 (single drive) | `smm-data` |

**Minimum host for the MVP:** 4 vCPU / 8 GiB / 80 GiB SSD runs this stack with
**3 workers**. The 7-node sizing in INFRA_ANALYST §3.1 is the 50-pod target;
do not start there.

## 2. Phase 1 — host

```bash
sudo apt-get update && sudo apt-get install -y curl ca-certificates jq openssl git

# k3s: single-node control plane + containerd, Traefik disabled (ingress-nginx instead)
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--write-kubeconfig-mode 640 --disable traefik" sh -
sudo install -m 0755 /usr/local/bin/k3s /usr/local/bin/kubectl

kubectl get nodes                                   # Gate 2.1
curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | sudo bash
```

**Gate 2.1** — one node, `Ready`. Not ready? `sudo journalctl -u k3s -n 100`;
stop here.

## 3. Phase 2 — cluster add-ons

```bash
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm repo add jetstack https://charts.jetstack.io
helm repo update

helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx \
  -n ingress-nginx --create-namespace \
  --set controller.config.proxy-buffering=off

helm upgrade --install cert-manager jetstack/cert-manager \
  -n cert-manager --create-namespace --set crds.enabled=true
```

`proxy-buffering=off` is load-bearing: the dashboard consumes SSE on
`/api/stream` and buffered events make the UI look dead
([ADR-0010](adr/0010-sse-over-websocket.md)).

**Gate 3.1** — `kubectl get pods -n ingress-nginx` and `-n cert-manager` are
all `Running`, and `kubectl get storageclass` shows a **default** class
(k3s ships `local-path`).

## 4. Phase 3 — namespace, RBAC, secrets

```bash
cd /srv/mvp-1-smm          # repo checkout on the host

kubectl apply -f infra/k8s/00-namespace.yaml    # Namespace + ResourceQuota + LimitRange
kubectl apply -f infra/k8s/10-rbac.yaml         # SA smm-provisioner + Role/RoleBinding

kubectl -n smm get quota,limitrange,sa smm-provisioner   # Gate 4.1
```

The SA name is not cosmetic: `ensurePod` hard-codes
`ServiceAccountName: "smm-provisioner"`; a rename means pods are created but
never scheduled.

### 4.1 Secrets — generate on the host, never commit

```bash
cd /srv/mvp-1-smm
umask 077
cat > infra/k8s/secrets.env <<EOF
JWT_SECRET=$(openssl rand -hex 32)
CREDENTIAL_KEY=$(openssl rand -base64 32)
INTERNAL_API_SECRET=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 24)
MINIO_ROOT_USER=smm
MINIO_ROOT_PASSWORD=$(openssl rand -hex 24)
DASHBOARD_DOMAIN=smm.example.com
SEED_ADMIN_EMAIL=owner@example.com
SEED_ADMIN_PASSWORD=$(openssl rand -base64 18)
EOF

set -a; . infra/k8s/secrets.env; set +a
kubectl -n smm create secret generic smm-secrets \
  --from-literal=JWT_SECRET="$JWT_SECRET" \
  --from-literal=CREDENTIAL_KEY="$CREDENTIAL_KEY" \
  --from-literal=INTERNAL_API_SECRET="$INTERNAL_API_SECRET" \
  --from-literal=POSTGRES_PASSWORD="$POSTGRES_PASSWORD" \
  --from-literal=MINIO_ROOT_USER="$MINIO_ROOT_USER" \
  --from-literal=MINIO_ROOT_PASSWORD="$MINIO_ROOT_PASSWORD"

kubectl -n smm get secret smm-secrets -o jsonpath='{.data.CREDENTIAL_KEY}' \
  | base64 -d | wc -c        # Gate 4.2: prints 32
```

`CREDENTIAL_KEY` must decode to **exactly 32 bytes** or the API refuses to boot
(`config.Validate`). It encrypts every stored account credential: back it up
out-of-band now — losing it makes existing credentials unrecoverable.

## 5. Phase 4 — data plane

`infra/k8s/20-data.yaml` runs Postgres+Timescale, Redis and MinIO as
single-replica StatefulSets with PVCs (100 GiB / 5 GiB / 500 GiB). Image tags
match `compose.yaml`; Service names match the defaults baked into the images.

The SPOFs are accepted for the MVP ([INFRA_ANALYST](INFRA_ANALYST.md) §6.2):
Postgres single replica, Redis single, MinIO single-drive (the compose 4-drive
layout is the v1 topology).

```bash
kubectl apply -f infra/k8s/20-data.yaml
kubectl -n smm rollout status statefulset/postgres --timeout=300s
kubectl -n smm rollout status statefulset/redis    --timeout=180s
kubectl -n smm rollout status statefulset/minio    --timeout=180s
kubectl -n smm exec postgres-0 -- pg_isready -U smm -d smm   # Gate 5.1
```

## 6. Phase 5 — schema

The API does not migrate on boot. `infra/k8s/30-migrate.yaml` is the one-shot
Job equivalent of compose's `migrate` service.

```bash
kubectl -n smm create configmap smm-migrations --from-file=apps/api/db/migrations
kubectl -n smm delete job migrate --ignore-not-found
kubectl apply -f infra/k8s/30-migrate.yaml
kubectl -n smm wait --for=condition=complete job/migrate --timeout=300s   # Gate 6.1
```

Job names are immutable — the `delete` is required on every re-run. If it
loops, `kubectl -n smm logs job/migrate`; a dirty version is a
[BACKUP.md](BACKUP.md) situation, not a retry.

## 7. Phase 6 — images

The web image bakes `NEXT_PUBLIC_API_URL`/`NEXT_PUBLIC_SSE_URL` at build time
(`apps/web/Dockerfile`); a wrong value is invisible until the dashboard loads and
every request fails.

```bash
cd /srv/mvp-1-smm
set -a; . infra/k8s/secrets.env; set +a
REG=ghcr.io/<owner>; TAG=$(git rev-parse --short HEAD)
B="docker buildx build --platform linux/amd64 --push"

$B -t "$REG/smm-api:$TAG"    -f apps/api/Dockerfile apps/api
$B -t "$REG/smm-worker:$TAG" -f apps/worker/Dockerfile .
$B -t "$REG/smm-web:$TAG"    -f apps/web/Dockerfile . \
  --build-arg NEXT_PUBLIC_API_URL="https://$DASHBOARD_DOMAIN" \
  --build-arg NEXT_PUBLIC_SSE_URL="https://$DASHBOARD_DOMAIN/api/stream"
```

Workers dial `http://api:8080` in-cluster; the **browser** dials the ingress.
Those are different values on purpose — do not unify them.

Single-node without a registry: `docker save <img> | sudo k3s ctr images import -`
and set `imagePullPolicy: IfNotPresent`.

**Gate 7.1** — all three images resolve (by digest) on the node.

## 8. Phase 7 — API + web + ingress

Substitute the placeholders **before** applying (the manifests are templates):

```bash
cd /srv/mvp-1-smm
set -a; . infra/k8s/secrets.env; set +a

sed -e "s|IMAGE_PLACEHOLDER|$REG/smm-api:$TAG|g" \
    -e "s|WORKER_IMAGE_PLACEHOLDER|$REG/smm-worker:$TAG|g" \
    -e "s|DASHBOARD_DOMAIN_PLACEHOLDER|$DASHBOARD_DOMAIN|g" \
    infra/k8s/40-api.yaml | kubectl apply -f -

sed -e "s|WEB_IMAGE_PLACEHOLDER|$REG/smm-web:$TAG|g" \
    infra/k8s/40-web.yaml | kubectl apply -f -

sed -e "s|DASHBOARD_DOMAIN_PLACEHOLDER|$DASHBOARD_DOMAIN|g" \
    infra/k8s/50-ingress.yaml | kubectl apply -f -

kubectl -n smm rollout status deploy/api --timeout=240s
kubectl -n smm rollout status deploy/web --timeout=240s
```

**Gate 8.1 — serving.** `https://$DASHBOARD_DOMAIN/healthz` → 200 and `/login`
renders. TLS needs DNS pointing at the node and ports 80/443 open.

**Gate 8.2 — the provisioner is the k8s driver.** This is the failure that looks
like a broken dashboard:

```bash
kubectl -n smm logs deploy/api --tail=50 | grep 'provisioner:'
```

Must say `provisioner: k8s driver`. `static driver` means `PROVISIONER_MODE` did
not reach the pod; nothing will ever create a worker.

## 9. Phase 8 — seed owner, create a worker

```bash
cd /srv/mvp-1-smm
set -a; . infra/k8s/secrets.env; set +a
PW=$(kubectl -n smm get secret smm-secrets -o jsonpath='{.data.POSTGRES_PASSWORD}' | base64 -d)

kubectl -n smm run seed --rm -it --restart=Never \
  --image="$REG/smm-api:$TAG" \
  --env="DATABASE_URL=postgres://smm:$PW@postgres:5432/smm?sslmode=disable" \
  --env="SEED_ADMIN_EMAIL=$SEED_ADMIN_EMAIL" \
  --env="SEED_ADMIN_PASSWORD=$SEED_ADMIN_PASSWORD" \
  -- /app/server seed
```

Seed is idempotent: an existing email is reported, not overwritten. Then log in
and use **Workers → Add container**; the reconciler provisions within one tick
(`RECONCILE_INTERVAL_SECONDS=5`).

**Gate 9.1 — resources exist**

```bash
kubectl -n smm get pods -l smm.worker-id          # Running
kubectl -n smm get svc  -l smm.worker-id          # ClusterIP :6080
kubectl -n smm get pvc | grep smm-session
kubectl -n smm logs -l smm.worker-id --tail=20    # "worker entrypoint ready" → "worker starting"
```

**Gate 9.2 — the worker bound the row.** A row is only bound when
`/internal/claim` succeeds:

```bash
kubectl -n smm exec postgres-0 -- psql -U smm -d smm -c \
  "select id, name, container_id is not null as bound, status, last_heartbeat from worker;"
```

`bound = t` and `last_heartbeat` advancing. If not: check `API_URL` resolves
from the worker pod (the image has `wget`, no `curl`) and that the API's
`INTERNAL_API_SECRET` state is intentional.

## 10. Known gaps — read before promising a feature

| # | Gap | Consequence | Concrete fix |
| --- | --- | --- | --- |
| 10.1 | noVNC is `ClusterIP`-only; the browser cannot reach it | dashboard live view disabled; 2FA logins uncompletable in-UI | API-side authenticated proxy for noVNC, or per-worker ingress + auth — product decision, then INFRA-08 |
| 10.2 | k8s driver injects **no env** into worker pods (`ensurePod`) | `INTERNAL_API_SECRET`, `ACTION_DRY_RUN=false`, `PROXY_URL`, `NOVNC_URL` never arrive; fleet stays dry-run and unproxied | add `Env []corev1.EnvVar` to `k8s.Config`, append in `ensurePod`; pass proxy per worker like `dockerprovisioner` does |
| 10.3 | `StartWorker` calls `ensurePod` only, not `ensureService` | a resumed worker whose Service was deleted runs with no live view | call an idempotent `ensureService` from `StartWorker` |
| 10.4 | `NovncAllocator` parses a **host port** out of `NoVNCService` | meaningless in-cluster | leave allocator to the docker tier; k8s URL comes from the worker heartbeat via `/internal/claim` |
| 10.5 | PSA `baseline`, not `restricted` (INFRA_ANALYST §7) | weaker isolation than specified | validate the X stack under `restricted` on staging, then flip the namespace label |
| 10.6 | No NetworkPolicy yet (INFRA-04) | workers reach anything in-cluster; egress not FQDN-restricted | apply default-deny + the §5.3 matrix |
| 10.7 | No kube-prometheus/Loki (INFRA-10) | alerts in `infra/obs/alerts.yml` have no scraper | install kube-prometheus-stack, point it at API/worker `/metrics` |

**Until 10.2 is fixed, treat this tier as read-only mode:** scraping,
dashboards, monitoring and logins work; liking/commenting stays in
`ACTION_DRY_RUN=true` (the image default), so no platform action is fired.

## 11. Rollback

```bash
kubectl -n smm rollout undo deploy/api                     # bad image
kubectl -n smm set env deploy/api PROVISIONER_MODE=static  # stop provisioning, keep serving
kubectl -n smm exec postgres-0 -- \
  psql -U smm -d smm -c "select version, dirty from schema_migrations;"
```

Schema rollback mechanics: [BACKUP.md](BACKUP.md). A reconciler that will not
converge: [reconciler-drift](../infra/runbooks/reconciler-drift.md). A pod that
keeps dying: [worker-crashloop](../infra/runbooks/worker-crashloop.md).

## 12. Post-deploy checklist

- [ ] `kubectl -n smm get pods` — api 3/3, web 2/2, data 1/1 each
- [ ] `/healthz` 200, `/login` serves the dashboard over HTTPS
- [ ] API log shows `provisioner: k8s driver`
- [ ] Owner login succeeds; cookie is `Secure`
- [ ] Add worker → pod + Service + PVC appear; `worker.bound = t`; heartbeat advances
- [ ] `kubectl -n smm delete pod <worker>` → reconciler recreates it, session survives (same PVC)
- [ ] `ACTION_DRY_RUN` state recorded in the deploy ticket (gap 10.2)
- [ ] Backup path exercised against the cluster before any real account is added
