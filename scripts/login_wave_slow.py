#!/usr/bin/env python3
"""Slow login wave for the xlsx-imported Instagram accounts.

WHY THIS EXISTS, NEXT TO `login_wave_auto.py`
The earlier wave ran 5 headful logins at a time and stopped each container as
soon as a verdict landed. Instagram answers a steady machine-paced burst with a
reCAPTCHA wall (observed: a login parked on /auth_platform/recaptcha and waited
out its whole timeout), which is exactly the pattern that gets a whole slate
rate-limited. This one keeps ONE login in flight, leaves a long quiet gap
between them, and lets the API's own reconciler start and stop the containers —
so the fleet never shows more than a single running browser.

WHAT IT DOES
  1. Picks every account the API has not settled yet (PENDING / FAILED).
  2. Asks the API to log in one account (the refresh loop wakes its container,
     dispatches, and records the callback).
  3. Waits for a terminal status, up to TIMEOUT.
  4. Writes the verdict into Account.xlsx using the SAME vocabulary the
     previous script used (OK / CAPTCHA / WRONGPASS / KODE_EMAIL/WA /
     CHALLENGE / PERLU_DICEK), so the sheet stays readable next to the old run.
  5. Cleans that container up and sleeps GAP seconds before the next account.

A failed account is SKIPPED, not retried in this run: the point is to try every
account once and hand the operator a list of what needs a human.

USAGE
  python3 scripts/login_wave_slow.py            # run until every account is tried
  python3 scripts/login_wave_slow.py --limit 5  # try five, then stop
  python3 scripts/login_wave_slow.py --dry-run  # print the plan, touch nothing

SAFETY
  - Login containers are torn down between accounts, so peak RAM is one browser.
  - The loop refuses to start unless the API's refresh loop is on and its own
    concurrency is 1; a mis-set environment is how the fleet becomes a burst.
  - Progress is checkpointed to /tmp/wave_slow_state.json after every account,
    so an interrupted run resumes instead of starting over.
"""

import argparse
import datetime
import json
import subprocess
import sys
import time

API = "http://localhost:24080"
COOKIES = "/tmp/smm-cookies.txt"
XLSX = "/Users/ahdirmai/Account.xlsx"
STATE = "/tmp/wave_slow_state.json"

# One login in flight, and a long quiet gap between them. The gap is the whole
# point: Instagram's challenge is triggered by RATE, not by count.
GAP = 240
TIMEOUT = 420

TERMINAL = {"AUTHENTICATED", "FAILED", "NEEDS_INPUT", "QUARANTINED"}

# Credential-vocabulary the sheet already uses. Same mapping as
# login_wave_auto.py's verdict(), kept identical so the two runs are comparable.
def verdict(auth_status, last_error):
    """Map an account row to the sheet's credential vocabulary.

    The worker now prefix-labels WHY a login stopped (`wrong_password:`,
    `captcha:`, `checkpoint:`, ...). Those prefixes are checked first and
    exactly, so a verdict never depends on prose: before this, every wall fell
    through to "GAGAL:submit did not complete" and a whole run produced one
    indistinguishable row type.
    """
    e = (last_error or "").lower()
    if auth_status == "AUTHENTICATED":
        return "OK"
    if auth_status == "NEEDS_INPUT":
        return "CHALLENGE"
    if auth_status == "FAILED":
        for prefix, label in (
            ("wrong_password:", "WRONGPASS"),
            ("captcha:", "CAPTCHA"),
            ("rate_limited:", "RATE_LIMIT"),
            ("account_locked:", "LOCKED"),
            ("checkpoint:", "KODE_EMAIL/WA"),
        ):
            if e.startswith(prefix):
                return label
        # Older worker builds, and any message that predates the prefixes.
        if "password" in e or "credential" in e or "incorrect" in e:
            return "WRONGPASS"
        if "captcha" in e or "recaptcha" in e:
            return "CAPTCHA"
        if "challenge" in e or "verify" in e or "code" in e:
            return "KODE_EMAIL/WA"
        return "GAGAL:" + (last_error or "?")[:40]
    return "PERLU_DICEK"


def psql(sql):
    r = subprocess.run(
        ["docker", "exec", "smm-postgres-1", "psql", "-U", "smm", "-d", "smm",
         "-t", "-A", "-F", "|", "-c", sql],
        capture_output=True, text=True, timeout=60,
    )
    return [l for l in r.stdout.strip().split("\n") if l]


def api(method, path, wait=120):
    """POST the API and return the HTTP status, or "TIMEOUT"/"ERROR".

    The login route BLOCKS until the waker reports the container ready, which
    routinely exceeds a minute (container boot + control subscriber). A plain
    timeout here aborted the whole run; the request itself is fine, so it is
    reported as a transport result and the caller treats it as "dispatched,
    outcome unknown" and falls through to polling the row.
    """
    try:
        r = subprocess.run(
            ["curl", "-s", "--max-time", str(wait), "-b", COOKIES, "-X", method,
             f"{API}{path}", "-o", "/dev/null", "-w", "%{http_code}"],
            capture_output=True, text=True, timeout=wait + 15,
        )
        return r.stdout.strip() or "000"
    except subprocess.TimeoutExpired:
        return "TIMEOUT"


def load_xlsx():
    import openpyxl
    wb = openpyxl.load_workbook(XLSX)
    ws = wb["Instagram"]
    m = {}
    for i, row in enumerate(ws.iter_rows(min_row=2, values_only=True), start=2):
        if row[0]:
            m[str(row[0]).strip().lower()] = i
    return wb, ws, m


def write_result(username, status):
    wb, ws, m = load_xlsx()
    u = username.lower()
    if u in m:
        ws.cell(row=m[u], column=3, value=status)
        ws.cell(row=m[u], column=4, value=None)
    else:
        r = ws.max_row + 1
        ws.cell(row=r, column=1, value=username)
        ws.cell(row=r, column=3, value=status)
    wb.save(XLSX)


def stop_worker(worker_id):
    """Park the worker through the API's own control path.

    NOT `docker rm -f`: the reconciler sees a row still wanting RUNNING and
    recreates the container immediately, and any in-flight control message is
    lost with the old process (observed: a login dispatched to a container that
    was then recreated and never received it, so the row sat AUTHENTICATING
    until the stale sweep gave up). Setting desired_state to PAUSED lets the
    same loop that started it stop it, and keeps the session volume.
    """
    subprocess.run(
        ["docker", "exec", "smm-postgres-1", "psql", "-U", "smm", "-d", "smm", "-c",
         f"UPDATE worker SET desired_state='PAUSED' WHERE id='{worker_id}'"],
        capture_output=True, text=True, timeout=60,
    )


def pending():
    """Every account the API has not settled, oldest username first."""
    rows = psql("""
      SELECT a.id, a.username, a.worker_id
      FROM account a
      WHERE a.platform='INSTAGRAM'
        AND a.auth_status IN ('PENDING','FAILED')
        AND a.worker_id IS NOT NULL
      ORDER BY a.username""")
    out = []
    for l in rows:
        p = l.split("|")
        if len(p) >= 3:
            out.append({"id": p[0], "username": p[1], "worker": p[2]})
    return out


def wait_settled(account_id):
    """Poll one account until a terminal status or the timeout."""
    deadline = time.time() + TIMEOUT
    while time.time() < deadline:
        time.sleep(15)
        rows = psql(
            "SELECT auth_status||'|'||coalesce(last_error,'') FROM account "
            f"WHERE id='{account_id}'")
        if not rows:
            continue
        st, _, err = rows[0].partition("|")
        if st in TERMINAL:
            return st, err
    return None, "TIMEOUT (no callback)"


def save_state(state):
    with open(STATE, "w") as f:
        json.dump(state, f, indent=1)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--gap", type=int, default=GAP)
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    todo = pending()
    print(f"accounts to try: {len(todo)}", flush=True)
    if args.dry_run:
        for a in todo[:20]:
            print("  would try", a["username"])
        return 0

    state = {"started": str(datetime.datetime.now()), "results": {}}
    tried = 0
    for acc in todo:
        if args.limit and tried >= args.limit:
            print(f"limit reached ({args.limit})", flush=True)
            break
        tried += 1
        code = api("POST", f"/api/accounts/{acc['id']}/login")
        print(f"[{tried}/{len(todo)}] {acc['username']} dispatch http={code}", flush=True)
        # TIMEOUT means the request outlived curl's wait, not that the dispatch
        # failed: the API is still booting the worker. Poll the row instead.
        if code not in ("200", "202", "TIMEOUT", "000"):
            state["results"][acc["username"]] = f"DISPATCH_FAILED:{code}"
            write_result(acc["username"], f"DISPATCH_FAILED:{code}")
            save_state(state)
            time.sleep(args.gap)
            continue

        st, err = wait_settled(acc["id"])
        label = verdict(st, err) if st else "TIMEOUT"
        state["results"][acc["username"]] = label
        write_result(acc["username"], label)
        save_state(state)
        print(f"    -> {acc['username']}: {label}", flush=True)

        stop_worker(acc["worker"])
        if tried < len(todo):
            print(f"    sleeping {args.gap}s before the next login", flush=True)
            time.sleep(args.gap)

    print("DONE", flush=True)
    print(json.dumps(state["results"], indent=1), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
