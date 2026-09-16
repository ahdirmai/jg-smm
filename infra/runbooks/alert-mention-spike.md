# ALERT: alert-mention-spike

**Severity:** SEV2 (critical tier when ≥ 6x) — an official account's mentions
exceeded 3x the trailing 7-day daily average.
**Alert source:** `AlertEngine.checkMentionSpike`
(`apps/api/internal/service/alert_engine.go`), rule `mention_spike`. Enabled by
`ALERT_INTERVAL_SECONDS`. The engine logs `alert fired kind=mention_spike`.

## Symptoms

- API logs contain `alert fired` with `kind=mention_spike`,
  `subject=account:<id>`.
- The dashboard (SSE) surfaces the alert if the stream is connected.
- The message names the account, the multiple, the baseline and the counts:
  `@handle mentions 4.2x the 7-day daily average (126 vs 30)`.

## Impact

Reputational/brand risk: a sudden mention surge usually means the account was
tagged by a large account, picked up a controversy, or is being mass-mentioned
by a botnet. Automated actions (comments) during a surge are a risk amplifier —
an automated reply in a controversy reads worse than no reply.

## Likely causes

1. **Genuine virality** — a real surge from a large mentioner. The signal is
   correct; decide whether to *pause* automated engagement.
2. **Bot/scrape noise** — a botnet or a scraper inflating mention counts; the
   numbers are real, the sentiment is not.
3. **Baseline too small** — a young account: `avgBase` is near zero, so a small
   absolute surge reads as a huge multiple. The engine skips `avgBase == 0`, but
   a 1-mention baseline times 4 is still 4x.
4. **Analytics provider misconfiguration** — `ANALYTICS_PROVIDER` returning
   wrong counts (check `analytics_ingest_run` for a suspicious jump).
5. **Clock/baseline skew** — the 7-day window is computed at evaluation time; a
   long ingest gap makes the recent window look dense by comparison.

## Diagnosis

```bash
# 1. The fired alert, with the exact numbers.
make logs S=api | grep 'mention_spike' | tail -20

# 2. Is the alert engine even running? (off by default)
docker compose exec -T api printenv ALERT_INTERVAL_SECONDS
make logs S=api | grep 'alert engine started'

# 3. The account and its recent snapshots.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select id, handle, platform, status from official_account;"
#    Replace <id> with the account id from the alert subject:
docker compose exec -T postgres psql -U smm -d smm -c \
  "select ts, mentions from analytics_snapshot where account_id = '<id>' order by ts desc limit 24;"

# 4. Same story from the mention-level table, to see whether it is one source
#    or many (one source = bot/viral single point; many = real spread):
docker compose exec -T postgres psql -U smm -d smm -c \
  "select date_trunc('hour', ts) as h, count(*) from analytics_mention where account_id = '<id>' and ts > now() - interval '24 hours' group by h order by h;"

# 5. Was there an ingest anomaly feeding the spike?
docker compose exec -T postgres psql -U smm -d smm -c \
  "select id, provider, started_at, finished_at, status from analytics_ingest_run order by started_at desc limit 5;"

# 6. Is the account still climbing, or did it settle?
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) from analytics_mention where ts > now() - interval '1 hour' and account_id = '<id>';"
```

## Remediation

```bash
# A. Virality — pause automated engagement for the account rather than letting
#    the scheduler comment into a surge.
ACTION_DRY_RUN=true make up      # global: stops all action commits
#    Or scope to the account by pausing its jobs, then resume once the surge
#    settles:
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) from action_job where account_id = '<id>' and status not in ('done','failed');"

# B. Bot noise — the data is polluted, so the alert will keep firing. Quiesce
#    by correcting the bad snapshots is NOT worth it; instead widen the
#    baseline or threshold in AlertEngineConfig, and file the provider issue.
#    The threshold is code, not env — see alert_engine.go MentionSpikeMultiple.

# C. Tiny-baseline false positive: this is expected behaviour for young
#    accounts (the engine intentionally skips a zero baseline). Confirm with
#    step 3 above that avgBase is genuinely small, then treat as SEV4 noise.

# D. Provider misconfiguration — the provider is returning garbage:
docker compose exec -T api printenv ANALYTICS_PROVIDER ANALYTICS_PROVIDER_BASE_URL
#    Fix the provider key/URL in .env, then:
make down && make up
```

## Verification

```bash
# No new mention_spike alerts since remediation:
make logs S=api --since 20m | grep 'mention_spike' | tail -5 || echo 'no new alerts'
# Mention rate back inside the baseline:
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) from analytics_mention where account_id = '<id>' and ts > now() - interval '1 hour';"
# The engine ticked and evaluated cleanly (no check-failed warnings):
make logs S=api | grep -E 'alert engine|mention-spike check failed' | tail -10
```

## Post-mortem prompt

Real surge or measurement artifact? If the baseline was tiny, the alert is
correct-but-noise — the tuning question is whether young accounts should be a
severe tier. See [README](README.md#post-mortem-prompt).
