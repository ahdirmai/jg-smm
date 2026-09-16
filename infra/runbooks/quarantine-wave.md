# ALERT: quarantine-wave

**Severity:** SEV1 — more than 5 accounts auto-quarantined within an hour. The
health model is doing its job; something is breaking accounts in bulk.
**Alert source:** Prometheus `QuarantinedAccountsRising`
(infra/obs/alerts.yml): `increase(smm_accounts_total{status="quarantined"}[1h]) > 5`.
**Metric:** `smm_accounts_total{status="quarantined"}`, driven by `applyHealth`
in `internal/service/health_score_wire.go`.

## Symptoms

- Account list shows a growing `QUARANTINED` block, clustered in time.
- `smm_accounts_total{status="quarantined"}` stepped up more than 5 in the hour.
- Newly quarantined accounts share one `error_class` (the quarantine threshold
  is reached by repeated failure of the same kind).
- If the class is `BANNED`, the accounts are `DEAD`, not quarantined — check
  both statuses; a ban wave is SEV1 with a different remediation.

## Impact

Capacity is leaving the pool automatically and will not come back on its own.
If the cause is still active, the remaining healthy accounts are next; a
quarantine wave unaddressed becomes a ban wave.

## Likely causes

1. **Platform throttle / soft ban wave** — the platform flagged the posting
   pattern (timing, text similarity, or a shared proxy range). All accounts on
   the affected range fail identically.
2. **Shared proxy range burned** — one residential pool's IPs got flagged;
   every account on that `proxy_group_id` fails with `AUTH`/`BANNED`.
3. **Template pool flagged** — the same comment text across many accounts is
   the classic bulk-detection trigger; failures cluster after a template push.
4. **Posting cadence too high** — health fell on `RATE_LIMIT` failures; the
   cooldown is per-account but the platform's limit is per-IP.

## Diagnosis

```bash
# 1. What got quarantined, and on what class?
docker compose exec -T postgres psql -U smm -d smm -c \
  "select username, platform, health_score, last_error, proxy_group_id, worker_id
   from account where status = 'QUARANTINED' order by last_checked desc;"

# 2. Cluster by proxy group: a shared group = a shared IP range = a shared cause.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select coalesce(proxy_group_id,'(none)') as grp, count(*) as quarantined
   from account where status = 'QUARANTINED' group by grp order by quarantined desc;"

# 3. Cluster by worker: one container = one browser session volume.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select coalesce(worker_id,'(unassigned)') as w, count(*) as quarantined
   from account where status = 'QUARANTINED' group by w order by quarantined desc;"

# 4. Are they DEAD instead? A ban wave needs different remediation.
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select status, count(*) from account where status in ('QUARANTINED','DEAD') group by status;"

# 5. What text did they post? Similarity across accounts = template detection.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select rendered_text, count(*) as uses from action_log
   where created_at > now() - interval '3 hours' and rendered_text is not null
   group by rendered_text order by uses desc limit 10;"
```

## Remediation

```bash
# A. STOP the fleet first. A quarantine wave means the cause is still active;
#    continuing to post with the surviving accounts is how they get banned too.
make down   # or: docker compose stop worker

# B. Shared proxy group identified (step 2): move the quarantined accounts off
#    that range before resuming. The proxy group is a per-account field:
docker compose exec -T postgres psql -U smm -d smm -c \
  "update account set proxy_group_id = '<OTHER_GROUP>' where status = 'QUARANTINED' and proxy_group_id = '<BURNED_GROUP>';"

# C. Template similarity (step 5): rotate the pool, raise per-template variety,
#    then resume a FEW accounts by hand and watch them for 30 minutes.

# D. Resume accounts individually after the cause is addressed — resume resets
#    the health score to 100, so only resume an account you believe in.
#    One at a time, verifying each posts successfully before the next.
```

## Verification

```bash
# No new quarantines in the window (this is the real signal):
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=increase(smm_accounts_total{status="quarantined"}[1h])'
# Resumed accounts holding their score:
curl -s localhost:24090/api/v1/query --data-urlencode 'query=smm_worker_health_score'
```

## Post-mortem prompt

What did the quarantined accounts share: proxy group, worker, template text, or
nothing? Shared = environmental; scattered = per-account sessions. See
[README](README.md#post-mortem-prompt).
