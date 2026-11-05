#!/usr/bin/env python3
"""Resumable, serial fleet login test. Never reads platform passwords."""
import argparse
import collections
import csv
import datetime as dt
import fcntl
import http.cookiejar
import json
import os
from pathlib import Path
import re
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from zoneinfo import ZoneInfo

ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / '.local-runtime/fleet-login-test-2026-11-04'
STATE = OUTPUT / 'state.json'
API = 'http://localhost:24080'
TERMINAL = {'AUTHENTICATED', 'FAILED', 'QUARANTINED'}


def now():
    return dt.datetime.now(ZoneInfo('Asia/Makassar')).isoformat()


def command(args, **kwargs):
    return subprocess.run(args, text=True, capture_output=True, check=True, timeout=90, **kwargs).stdout.strip()


def sql(query):
    return command(['docker', 'exec', 'smm-postgres-1', 'psql', '-U', 'smm', '-d', 'smm', '-At', '-v', 'ON_ERROR_STOP=1', '-c', query])


def rows(account_id=None):
    where = '' if account_id is None else " WHERE a.id='%s'" % str(uuid.UUID(account_id))
    query = """SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM (
      SELECT a.id,a.username,lower(a.platform::text) AS platform,a.auth_status,
        a.last_error,a.worker_id,a.last_checked_at,a.last_verified_at,
        w.novnc_service AS live_url,w.current_job_id
      FROM account a LEFT JOIN worker w ON w.id=a.worker_id""" + where + ' ORDER BY a.platform,a.username) t'
    return json.loads(sql(query))


def clean(value):
    return re.sub(r'(https?://[^\s?#]+)[?#][^\s]*', r'\1', value or '')[:1000]


def verdict(account):
    status, reason = account['auth_status'], account.get('last_error') or ''
    if status == 'AUTHENTICATED':
        return 'OK'
    for prefix, result in [('otp_required:', 'PERLU_OTP'), ('wrong_password:', 'WRONG_PASSWORD'),
                           ('rate_limited:', 'RATE_LIMITED'), ('account_locked:', 'ACCOUNT_LOCKED')]:
        if reason.startswith(prefix):
            return result
    if status == 'NEEDS_INPUT':
        return 'CAPTCHA' if reason.startswith('captcha:') else 'MANUAL_CHALLENGE'
    if reason.startswith('navigation failed:'):
        return 'NAVIGATION_ERROR'
    return status


def dispatch_confirmed(account, current):
    if current.get('http_status') in (200, 202):
        return True
    checked = account.get('last_checked_at')
    if not checked:
        return False
    return dt.datetime.fromisoformat(checked) >= dt.datetime.fromisoformat(current['started_at']) - dt.timedelta(seconds=2)


def save(state):
    state['updated_at'] = now()
    state['done'] = len(state['results'])
    state['counts'] = dict(collections.Counter(r['verdict'] for r in state['results'].values()))
    temporary = STATE.with_suffix('.tmp')
    temporary.write_text(json.dumps(state, indent=2, ensure_ascii=False))
    temporary.replace(STATE)
    destination = OUTPUT / 'results.csv'
    temporary = destination.with_suffix('.tmp')
    fields = ['username', 'platform', 'verdict', 'reason', 'started_at', 'finished_at', 'evidence', 'live_url', 'screenshot']
    with temporary.open('w', newline='') as stream:
        writer = csv.DictWriter(stream, fieldnames=fields, extrasaction='ignore')
        writer.writeheader()
        writer.writerows(state['results'].values())
    temporary.replace(destination)
    if state.get('infrastructure_failures'):
        with (OUTPUT / 'infrastructure-errors.csv').open('w', newline='') as stream:
            writer = csv.DictWriter(stream, fieldnames=fields, extrasaction='ignore')
            writer.writeheader()
            writer.writerows(state['infrastructure_failures'])


class Client:
    def __init__(self):
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.login()

    def post(self, path, body=None, renew=True):
        request = urllib.request.Request(API + path, data=json.dumps(body or {}).encode(),
                                         headers={'Content-Type': 'application/json'}, method='POST')
        try:
            with self.opener.open(request, timeout=135) as response:
                return response.status
        except urllib.error.HTTPError as error:
            if error.code == 401 and renew:
                self.login()
                return self.post(path, body, False)
            raise RuntimeError('HTTP %s: %s' % (error.code, clean(error.read().decode()[:500]))) from None

    def login(self):
        self.post('/api/auth/login', {'email': os.environ.get('SMM_LOGIN_TEST_ADMIN_EMAIL', 'owner@smm.local'),
                                      'password': os.environ.get('SMM_LOGIN_TEST_ADMIN_PASSWORD', 'changeme-changeme')}, False)


def stop_worker(account):
    if not account.get('worker_id'):
        return
    worker = str(uuid.UUID(account['worker_id']))
    changed = sql("UPDATE worker SET desired_state='PAUSED',status='IDLE',browser_status='cold',"
                  "last_error=CASE WHEN last_error LIKE 'otp_required:%%' THEN last_error "
                  "ELSE 'login_test_complete: account test finished' END "
                  "WHERE id='%s' AND current_job_id IS NULL AND desired_state<>'STOPPED' RETURNING id" % worker)
    if not changed:
        return
    ids = command(['docker', 'ps', '-q', '--filter', 'label=smm.worker.id=' + worker]).split()
    for container in ids:
        command(['docker', 'stop', '--time', '10', container])


def timeout_account(account):
    worker = str(uuid.UUID(account['worker_id']))
    command(['docker', 'exec', 'smm-redis-1', 'redis-cli', 'PUBLISH', 'control-' + worker,
             json.dumps({'type': 'auth-clear', 'accountId': account['id'], 'platform': account['platform']})])
    script = """const fs=require('node:fs');const body=fs.readFileSync(0,'utf8');
      fetch('http://127.0.0.1:8080/internal/account-callback',{method:'POST',
      headers:{'content-type':'application/json','x-internal-secret':process.env.INTERNAL_API_SECRET},body})
      .then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1));"""
    command(['docker', 'exec', '-i', 'smm-api-1', 'node', '-e', script], input=json.dumps({
        'accountId': account['id'], 'authStatus': 'FAILED',
        'error': 'login_test_timeout: no final callback within 15 minutes'}))


def capture(account, current):
    url = account.get('live_url')
    if not url:
        return
    path = OUTPUT / ('challenge-' + account['id'] + '.png')
    try:
        command(['node', str(ROOT / 'scripts/capture_worker_live.mjs'), url, str(path)])
        current['screenshot'] = str(path)
    except Exception:
        current['screenshot_error'] = 'Live capture unavailable; open live_url directly'
    current['live_url'] = url


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--plan-only', action='store_true')
    parser.add_argument('--gap', type=int, default=240)
    args = parser.parse_args()
    OUTPUT.mkdir(parents=True, exist_ok=True)
    lock = (OUTPUT / 'run.lock').open('w')
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    if STATE.exists():
        state = json.loads(STATE.read_text())
    else:
        plan = rows()
        for account in plan:
            account['last_error'] = clean(account.get('last_error'))
        state = {'started_at': now(), 'total': len(plan), 'plan': plan, 'results': {}, 'current': None, 'phase': 'ready'}
        # These two accounts already have live-login evidence from this chat.
        for account in plan:
            if ((account['username'] == 'farahmaysar' and account['auth_status'] == 'AUTHENTICATED') or
                (account['username'] == 'ahsan.zaky12' and (account.get('last_error') or '').startswith('otp_required:'))):
                state['results'][account['id']] = {**{k: account[k] for k in ['username', 'platform']},
                    'verdict': verdict(account), 'reason': clean(account.get('last_error')),
                    'finished_at': now(), 'evidence': 'previous_live_test_in_this_chat'}
        save(state)
    if args.plan_only:
        print(json.dumps({'total': state['total'], 'done': state['done'], 'remaining': state['total']-state['done']}))
        return
    client = Client()
    state['pid'] = os.getpid()
    state.pop('error', None)
    state['phase'] = 'waiting_manual_challenge' if (state.get('current') or {}).get('challenge_seen') else 'running'
    save(state)
    try:
        for planned in state['plan']:
            if planned['id'] in state['results']:
                continue
            while time.time() < state.get('next_attempt_after', 0):
                state['phase'] = 'cooldown'; save(state)
                # Saving can cross the deadline checked above. Never pass a
                # negative duration to sleep when the cooldown has just ended.
                time.sleep(max(0, min(30, state['next_attempt_after']-time.time())))
            account_id = planned['id']
            current = state.get('current')
            if not current or current['id'] != account_id:
                current = {'id': account_id, 'username': planned['username'], 'platform': planned['platform'],
                           'started_at': now(), 'deadline': time.time()+900, 'dispatch_attempted': False}
                state['current'] = current; state['phase'] = 'dispatching'; save(state)
            if not current['dispatch_attempted']:
                # Persist before sending: an interrupted request must not be duplicated on resume.
                current['dispatch_attempted'] = True; save(state)
                current['http_status'] = client.post('/api/accounts/' + account_id + '/login')
                if current['http_status'] not in (200, 202):
                    raise RuntimeError('Login dispatch was not accepted')
                state['phase'] = 'waiting'; save(state)
            while True:
                found = rows(account_id)
                if not found:
                    raise RuntimeError('Account disappeared from the plan: ' + account_id)
                account = found[0]
                status = account['auth_status']
                if not dispatch_confirmed(account, current):
                    raise RuntimeError('Interrupted dispatch is unconfirmed; review required before resuming ' + account_id)
                if status in TERMINAL:
                    if 'browser has been closed' in (account.get('last_error') or ''):
                        raise RuntimeError('Browser closed during login; inspect worker lifecycle before continuing ' + account_id)
                    if (account.get('last_error') or '').startswith('submit did not complete:'):
                        raise RuntimeError('Login form submission did not settle; inspect browser before continuing ' + account_id)
                    result = {k: account[k] for k in ['username', 'platform']}
                    result.update({'verdict': verdict(account), 'reason': clean(account.get('last_error')),
                                   'started_at': current['started_at'], 'finished_at': now(), 'evidence': 'live_login',
                                   'live_url': account.get('live_url')})
                    if current.get('screenshot'):
                        result['screenshot'] = current['screenshot']
                    if current.get('challenge_seen') and 'manual verification timed out' in result['reason']:
                        result['verdict'] = 'CAPTCHA_TIMEOUT' if current.get('challenge_reason', '').startswith('captcha:') else 'MANUAL_TIMEOUT'
                    stop_worker(account)
                    state['results'][account_id] = result
                    state['current'] = None
                    state['next_attempt_after'] = time.time() + (1800 if result['verdict'] == 'RATE_LIMITED' else args.gap)
                    save(state)
                    print('%s [%s/%s] %s: %s' % (now(),state['done'],state['total'],account['username'],result['verdict']),flush=True)
                    break
                if account.get('worker_id'):
                    # Authentication and a parked CAPTCHA are current browser
                    # activity; prevent idle suspension during the test.
                    worker = str(uuid.UUID(account['worker_id']))
                    sql("UPDATE worker SET last_action_at=now() WHERE id='%s' AND desired_state='RUNNING'" % worker)
                if status == 'NEEDS_INPUT' and not current.get('challenge_seen'):
                    current['challenge_seen'] = True
                    current['challenge_reason'] = clean(account.get('last_error'))
                    capture(account, current)
                    state['phase'] = 'waiting_manual_challenge'; save(state)
                    print(now(), 'Manual challenge:', account['username'], account.get('live_url'),flush=True)
                if status == 'AUTHENTICATING' and not current.get('diagnostic_capture') and time.time() > current['deadline'] - 840:
                    current['diagnostic_capture'] = True
                    capture(account, current)
                    save(state)
                if time.time() > current['deadline']:
                    timeout_account(account)
                time.sleep(5)
        state['phase'] = 'complete'; state['finished_at'] = now(); save(state)
        print('ALL ACCOUNTS TESTED',state['done'],flush=True)
    except Exception as error:
        state['phase'] = 'needs_attention'; state['error'] = clean(str(error)); save(state)
        raise


if __name__ == '__main__':
    main()
