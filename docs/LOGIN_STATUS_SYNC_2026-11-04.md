# Instagram login status reconciliation — 4 November 2026

## Applied result

Compared Account.xlsx, smm-ig/ig_verified.ndjson and
smm-verify/status_map.json against the live Instagram account records.
Source records are matched by normalized username. Source-file modification time
breaks ties among historical files; it is not treated as a login timestamp.
Passwords and raw source credentials are never copied into the report.

Existing checked database results and saved sessions take precedence. Only
PENDING records without a check timestamp or saved session were eligible.
Historical OK is mapped to NEEDS_INPUT for session verification, never directly
to AUTHENTICATED. Old password/technical failures map to FAILED; CAPTCHA/code/
challenge results map to NEEDS_INPUT. Previous DB verification without a saved
session also maps to NEEDS_INPUT. Login timestamps are not fabricated.

- Live Instagram accounts: 485; matching at least one historical source: 208.
- Updated 59 rows in one guarded transaction: 33 FAILED, 26 NEEDS_INPUT.
- Before/plan snapshots saved locally in `.local-runtime/login-sync/`.
- Immediate repeat dry run: zero additional changes.
- After: 274 PENDING, 178 FAILED, 26 NEEDS_INPUT, 6 AUTHENTICATING,
  1 AUTHENTICATED. Remaining PENDING accounts have no matching usable evidence
  under these reconciliation rules. No Instagram login was dispatched.

The 6 AUTHENTICATING rows were preserved, not certified as currently in flight;
reconciliation does not infer liveness from historical source files.

## Display fix

AccountService.List previously read only 200 newest accounts. It now reads every
page. Auth PENDING is included in OpenAPI-generated types and dashboard filters/
sorting. Historical results and common failure reasons appear below the auth
badge; query strings from challenge URLs are not rendered there.

## Verification

Go service/http/server tests passed, including a regression test retaining an
older authenticated account beyond the first 200 rows. Shared build and web
TypeScript checks passed. Rebuilt/restarted the local API container. Authenticated
GET /api/accounts returned all 486 accounts (485 Instagram, 1 Threads), matching
the DB counts and including 59 historical notes. /readyz reported all dependencies
up. Browser interaction and fresh Instagram session validity were not tested.

Repeat with `python3 scripts/reconcile_login_status.py` (dry run) or add `--apply`.
Paths may be overridden via CLI flags; credentials and snapshots stay local.
