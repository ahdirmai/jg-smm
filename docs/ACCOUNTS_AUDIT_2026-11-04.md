# Accounts completion audit — 4 November 2026

Audit scope: Accounts pagination, worker/noVNC readiness, login Conflict errors,
manual CAPTCHA, and the OTP skip/failure/shutdown policy.

## Verified behavior

- Pagination uses 25, 50 or 100 rows; filters reset the page, empty results are
  handled, and select-all applies to the current page.
- Legacy workers without a noVNC URL are provisioned before login validation.
  The failing farahmaysar request subsequently returned HTTP 202 and reached
  AUTHENTICATED with a saved session.
- Actionable API errors appear in Accounts instead of an unhandled error stack.
- CAPTCHA keeps the browser open for manual completion. A subsequent OTP
  challenge is skipped.
- OTP produces FAILED with `otp_required: perlu OTP`, disables automatic
  refresh and stops the container. The pool keeps the worker PAUSED until a
  new wake action. ahsan.zaky12 remains FAILED with its container off.

## Validation

| Check | Result |
| --- | --- |
| Go API tests and `go vet ./...` | Passed |
| Workspace lint and typecheck | Passed |
| Worker tests | 90 passed, zero failures |
| Repository tests against PostgreSQL/Timescale | 33 passed, zero failures |
| Production Next.js build in an isolated source copy | Passed |
| Pagination browser scenarios | 7 passed |
| OTP failure/off browser and Docker checks | Passed, including multiple pool ticks |
| Conflict feedback browser check | Passed, no unhandled JavaScript errors |

Database integration used a disposable database initialized with all 23
migrations. Fixtures now use database-generated IDs, create their own worker
dependency, clean up their own rows and schedule immediately claimable jobs
in the past to tolerate host/database clock differences.

Live platform login evidence covers farahmaysar and ahsan.zaky12. The remaining
fleet was covered through application tests and browser table checks; this
audit did not perform a bulk login run.

## Evidence

- `.local-runtime/repository-integration-audit-v2.log`
- `.local-runtime/accounts-pagination.png`
- `.local-runtime/accounts-otp-skipped.png`
- `.local-runtime/login-conflict-feedback.png`
