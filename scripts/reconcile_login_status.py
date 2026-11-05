#!/usr/bin/env python3
"""Reconcile historical Instagram results without declaring stale sessions live.

Dry run by default. --apply writes guarded updates in one transaction and saves
credential-free before/after plans under .local-runtime/login-sync/.
Existing checked results and active sessions are preserved. Source passwords are
never copied, printed, or stored in the report.
"""
import argparse
import datetime as dt
import json
from pathlib import Path
import subprocess
import xml.etree.ElementTree as ET
import zipfile

NS = {'s': 'http://schemas.openxmlformats.org/spreadsheetml/2006/main'}

def workbook_results(path):
    with zipfile.ZipFile(path) as z:
        strings = []
        if 'xl/sharedStrings.xml' in z.namelist():
            strings = [''.join(x.itertext()) for x in ET.fromstring(z.read('xl/sharedStrings.xml')).findall('s:si', NS)]
        for row in ET.fromstring(z.read('xl/worksheets/sheet1.xml')).findall('.//s:sheetData/s:row', NS)[1:]:
            cells = {}
            for c in row:
                col = c.get('r', '').rstrip('0123456789')
                if col not in ('A', 'C'):
                    continue
                v = c.find('s:v', NS)
                value = v.text if v is not None else ''.join(c.itertext())
                cells[col] = strings[int(value)] if c.get('t') == 's' else value
            if cells.get('A') and cells.get('C'):
                yield cells['A'], cells['C']

def historical_status(verdict):
    verdict = verdict.upper().strip()
    if verdict in ('OK', 'KODE_EMAIL/WA', 'EMAIL_CODE', 'CAPTCHA', 'CHALLENGE', 'PERLU_DICEK'):
        return 'NEEDS_INPUT'
    if verdict in ('WRONGPASS', 'ERR', 'TIMEOUT') or verdict.startswith(('GAGAL', 'DISPATCH_FAILED')):
        return 'FAILED'
    return None

def psql(sql):
    return subprocess.run(['docker', 'compose', 'exec', '-T', 'postgres', 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', 'smm', '-d', 'smm', '-tA'], input=sql, text=True, capture_output=True, check=True).stdout

def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--apply', action='store_true')
    ap.add_argument('--workbook', type=Path, default=Path('/Users/ahdirmai/Account.xlsx'))
    ap.add_argument('--ig-results', type=Path, default=Path('/Users/ahdirmai/smm-ig/ig_verified.ndjson'))
    ap.add_argument('--status-map', type=Path, default=Path('/Users/ahdirmai/smm-verify/status_map.json'))
    args = ap.parse_args()
    history = {}
    for source in sorted((args.workbook, args.ig_results, args.status_map), key=lambda p: p.stat().st_mtime):
        if source == args.workbook:
            entries = workbook_results(source)
        elif source == args.status_map:
            entries = json.loads(source.read_text()).items()
        else:
            entries = ((r['user'], r['status']) for r in (json.loads(line) for line in source.read_text().splitlines() if line.strip()))
        for user, verdict in entries:
            if historical_status(verdict):
                history[user.strip().lower().lstrip('@')] = (verdict, source.name)
    rows = json.loads(psql("SELECT coalesce(json_agg(x),'[]') FROM (SELECT a.id,a.username,a.auth_status,a.last_checked_at,a.last_verified_at,EXISTS(SELECT 1 FROM account_session s WHERE s.account_id=a.id) AS has_session FROM account a WHERE a.platform='INSTAGRAM') x;"))
    plan = []
    for r in rows:
        if r['auth_status'] != 'PENDING' or r['last_checked_at'] or r['has_session']:
            continue
        match = history.get(r['username'].strip().lower().lstrip('@'))
        if match:
            verdict, source = match
            status = historical_status(verdict)
            # Record file evidence as history, never as a newly verified session.
            note = f'historical_login_result: {verdict.split(":")[0]} ({source}); '
            note += 'previous login OK; session verification required' if verdict == 'OK' else 'operator retry or verification required'
        elif r['last_verified_at']:
            status = 'NEEDS_INPUT'
            note = 'historical_login_verified: previous verification recorded; no saved session; session verification required'
        else:
            continue
        plan.append({**r, 'new_status': status, 'note': note})
    stamp = dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    folder = Path('.local-runtime/login-sync') / stamp
    folder.mkdir(parents=True)
    (folder / 'before.json').write_text(json.dumps(rows, indent=2))
    (folder / 'plan.json').write_text(json.dumps(plan, indent=2))
    from collections import Counter
    print(json.dumps({'mode': 'apply' if args.apply else 'dry-run', 'instagram_accounts': len(rows), 'matched_sources': sum(r['username'].lower().lstrip('@') in history for r in rows), 'updates': len(plan), 'new_statuses': dict(Counter(r['new_status'] for r in plan)), 'report': str(folder)}))
    if args.apply and plan:
        payload = json.dumps(plan)
        assert '$sync$' not in payload
        sql = "BEGIN; WITH changes AS (SELECT * FROM json_to_recordset($sync$" + payload + "$sync$::json) AS x(id uuid,new_status text,note text)) UPDATE account a SET auth_status=c.new_status::auth_status,last_error=c.note FROM changes c WHERE a.id=c.id AND a.platform='INSTAGRAM' AND a.auth_status='PENDING' AND a.last_checked_at IS NULL AND NOT EXISTS(SELECT 1 FROM account_session s WHERE s.account_id=a.id); COMMIT;"
        print(psql(sql).strip())

if __name__ == '__main__':
    main()
