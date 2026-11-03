#!/usr/bin/env python3
"""SMM IG login wave — max 5 containers up at once (user rule).

Per wave of 5 (AUTO accounts, AUTHENTICATING only):
  1. POST /api/accounts/:id/login  (waker resumes the paused container)
  2. poll auth_status in DB until terminal (max 7 min)
  3. map verdict, docker stop the account's container
  4. append/update /Users/ahdirmai/Account.xlsx every wave
State file /tmp/wave_state.json for monitoring.
"""
import json, subprocess, time, datetime, sys

API = "http://localhost:24080"
COOKIES = "/tmp/smm-cookies.txt"
WAVE = 5
POLL_EVERY = 15
POLL_MAX = 28  # x 15s = 7 min

def psql(sql):
    r = subprocess.run(["docker","exec","smm-postgres-1","psql","-U","smm","-d","smm","-t","-A","-F","|","-c",sql],
                       capture_output=True, text=True, timeout=60)
    return [l for l in r.stdout.strip().split("\n") if l]

def api(method, path):
    r = subprocess.run(["curl","-s","-b",COOKIES,"-X",method,f"{API}{path}","-o","/dev/null","-w","%{http_code}"],
                       capture_output=True, text=True, timeout=60)
    return r.stdout.strip()

def stop_container(name):
    # worker.name is a prefix; the docker container carries a suffix
    r = subprocess.run(["docker","ps","-q","--filter",f"name=smm-worker-{name}"],
                       capture_output=True, text=True, timeout=60)
    cid = r.stdout.strip().split("\n")[0] if r.stdout.strip() else ""
    if cid:
        subprocess.run(["docker","stop","-t","0",cid], capture_output=True, text=True, timeout=90)

def verdict(auth_status, last_error):
    e = (last_error or "").lower()
    if auth_status == "AUTHENTICATED":
        return "OK"
    if auth_status == "NEEDS_INPUT":
        return "CHALLENGE"
    if auth_status == "FAILED":
        if "password" in e or "credential" in e or "incorrect" in e:
            return "WRONGPASS"
        if "captcha" in e:
            return "CAPTCHA"
        if "challenge" in e or "verify" in e or "code" in e:
            return "KODE_EMAIL/WA"
        return "GAGAL:" + (last_error or "?")[:40]
    return "PERLU_DICEK"

def load_xlsx_map():
    """username -> row index in Account.xlsx"""
    import openpyxl
    wb = openpyxl.load_workbook("/Users/ahdirmai/Account.xlsx")
    ws = wb["Instagram"]
    m = {}
    for i, row in enumerate(ws.iter_rows(min_row=2, values_only=True), start=2):
        if row[0]:
            m[str(row[0]).strip().lower()] = i
    return wb, ws, m

def write_result(username, status):
    import openpyxl
    wb, ws, m = load_xlsx_map()
    u = username.lower()
    if u in m:
        ws.cell(row=m[u], column=3, value=status)
        ws.cell(row=m[u], column=4, value=None)
    else:
        r = ws.max_row + 1
        ws.cell(row=r, column=1, value=username)
        ws.cell(row=r, column=2, value="katakunci777")
        ws.cell(row=r, column=3, value=status)
    wb.save("/Users/ahdirmai/Account.xlsx")

state = {"done": 0, "total": 0, "results": {}, "current_wave": [], "started": str(datetime.datetime.now())}

def save_state():
    json.dump(state, open("/tmp/wave_state.json","w"), indent=1)

def fetch_pending():
    rows = psql("""
      SELECT a.id, a.username, a.last_error, w.name
      FROM account a JOIN worker w ON w.id = a.worker_id
      WHERE w.source='AUTO' AND a.platform='INSTAGRAM' AND a.auth_status IN ('AUTHENTICATING')
      ORDER BY a.username""")
    out = []
    for l in rows:
        p = l.split("|")
        out.append({"id": p[0], "username": p[1], "last_error": p[2] if len(p)>2 else "", "container": p[3] if len(p)>3 else ""})
    return out

def main():
    pending = fetch_pending()
    state["total"] = len(pending)
    save_state()
    print(f"pending: {len(pending)}", flush=True)
    while True:
        pending = fetch_pending()
        if not pending:
            print("ALL DONE", flush=True)
            break
        wave = pending[:WAVE]
        state["current_wave"] = [a["username"] for a in wave]
        save_state()
        # dispatch
        for a in wave:
            code = api("POST", f"/api/accounts/{a['id']}/login")
            print(f"dispatch {a['username']} -> {code}", flush=True)
            time.sleep(3)
        # poll
        wave_start = time.time()
        remaining = {a["id"] for a in wave}
        for _ in range(POLL_MAX):
            time.sleep(POLL_EVERY)
            ids = "'" + "','".join(remaining) + "'"
            rows = psql(f"SELECT id||'|'||auth_status||'|'||coalesce(last_error,'') FROM account WHERE id IN ({ids})")
            for l in rows:
                p = l.split("|")
                aid, st, err = p[0], p[1], p[2] if len(p)>2 else ""
                if st in ("AUTHENTICATED","NEEDS_INPUT","FAILED","QUARANTINED"):
                    acct = next(x for x in wave if x["id"]==aid)
                    v = verdict(st, err)
                    state["results"][acct["username"]] = v
                    write_result(acct["username"], v)
                    stop_container(acct["container"])
                    remaining.discard(aid)
                    print(f"{acct['username']} -> {v}", flush=True)
                    save_state()
            if not remaining:
                break
        # timeout leftovers
        for aid in list(remaining):
            acct = next(x for x in wave if x["id"]==aid)
            state["results"][acct["username"]] = "TIMEOUT"
            write_result(acct["username"], "TIMEOUT")
            stop_container(acct["container"])
            remaining.discard(aid)
            print(f"{acct['username']} -> TIMEOUT", flush=True)
        state["done"] += len(wave)
        state["current_wave"] = []
        save_state()
        # settle between waves — let the 5 containers actually die before next 5
        time.sleep(30)
    save_state()

if __name__ == "__main__":
    main()
