# ALERT: worker-auth-failure

**Severity:** SEV2 — an account lost its platform session, or the login cannot
complete. Actions for that account cannot run safely.
**Alert source:** worker `core/auth.ts` returns outcome `rejected` or `failed`
on a login attempt; the worker reports it back through the
`/internal/action-callback` path. There is no dedicated page for this today —
it surfaces as account idleness and `worker.last_error`.

## Symptoms

- An account's actions stay `pending` while other accounts proceed.
- Worker logs contain `"outcome":"rejected"`, `"outcome":"failed"`, or
  repeated `auth-login` control messages for one account.
- `needs_input` loops: the same account re-enters a 2FA/checkpoint state every
  tick, with a screenshot written to the `screenshots` bucket each time.
- `worker.last_error` names the account; the noVNC live screen shows a login or
  checkpoint page.

## Impact

Bounded to the affected account. Sessions for other accounts are unaffected —
each platform has its own `session-<platform>.json` on the `sessions` volume.
The platform credential is never stored by the worker (the operator types it in
the headful session), so an auth failure is never a secret leak.

## Likely causes

1. **Session expired or invalidated** server-side by the platform (most common;
   nothing is broken — the cookie just died).
2. **2FA / checkpoint** requiring a human: outcome `needs_input`. Not a fault;
   it is the designed flow.
3. **`session-<platform>.json` corrupt or missing** on the `sessions` volume —
   a worker restart then needs a full re-login; see
   [worker-crashloop](worker-crashloop.md).
4. **Platform login flow changed** (selector drift) — the success signal is a
   *cookie*, never the URL, so URL changes do not cause false failures; a
   changed login page does.
5. **Wrong account mapping**: the control message addresses an account that has
   no parked context (`authContexts` miss → immediate `failed`).

## Diagnosis

```bash
# 1. Which accounts are stuck? worker rows point at the failing account.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select id, handle, platform, status from account order by updated_at desc limit 10;"
docker compose exec -T postgres psql -U smm -d smm -c \
  "select name, status, browser_status, current_job_id, last_error, last_action_at from worker;"

# 2. Worker auth logs: filter for the auth outcomes.
make logs S=worker | grep -E 'auth|outcome|verificationCode' | tail -40

# 3. Session files present and non-empty? (one per platform per worker)
docker compose exec -T worker sh -c 'ls -la /data/sessions/'

# 4. Is a login context parked awaiting input? (2FA / checkpoint)
make logs S=worker | grep -i 'needs_input\|parked'

# 5. The screenshot the worker took of the checkpoint page is the evidence:
docker compose run --rm --no-deps \
  -e MINIO_ROOT_USER="$(docker compose exec -T minio printenv MINIO_ROOT_USER)" \
  -e MINIO_ROOT_PASSWORD="$(docker compose exec -T minio printenv MINIO_ROOT_PASSWORD)" \
  --entrypoint /bin/sh minio-init -c 'mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"; mc ls --recursive local/screenshots | tail -10'

# 6. Session cookie validity — the success signal is a cookie, not a URL:
#    instagram needs sessionid + ds_user_id; threads needs sessionid.
make logs S=worker | grep -i 'sessionid\|proven'
```

## Remediation

```bash
# A. needs_input (2FA/checkpoint): a human completes the login in the live
#    browser. Attach to the worker's noVNC port (only exposed inside the net):
docker compose exec -T worker sh -c 'echo "noVNC on :6080 inside the container"'
#    then supply the code; the worker polls for the cookie and continues.

# B. Submit the 2FA code programmatically when the platform allows it
#    (auth-input control message via the control channel):
docker compose exec -T redis redis-cli publish control-<workerId> '<auth-input json>'

# C. Expired/invalid session: clear it and let the worker re-login.
docker compose exec -T worker sh -c 'rm -f /data/sessions/session-instagram.json'
docker compose restart worker
#    The worker re-enters the headful login; an operator completes it once.

# D. Corrupt sessions volume after a volume loss: restore from backup.
make restore B=latest      # restores sessions.tar onto the sessions volume

# E. Selector drift (login page changed): the platform's login page no longer
#    shows the expected fields. This is a code change in
#    apps/worker/src/core/auth.ts, not an ops fix — file it and fall back to
#    ACTION_DRY_RUN=true so no action is taken with a dead session.
```

## Verification

```bash
# The account's session is proven again (cookie present):
make logs S=worker | grep -i 'proven' | tail -5
# Actions for the account actually run (dry-run off, verdicts landing):
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select status, count(*) from action_job group by status;"
docker compose exec -T postgres psql -U smm -d smm -c \
  "select name, status, last_action_at from worker;"
# Session file is back and current:
docker compose exec -T worker sh -c 'ls -la /data/sessions/'
```

## Post-mortem prompt

Was the session loss expected (platform expiry) or caused by us (volume loss,
restart, selector drift)? If us: which runbook step should have prevented the
re-login cost. See [README](README.md#post-mortem-prompt).
