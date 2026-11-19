# local-worker harness

A standalone **local** dev harness that drives the real `apps/worker` Playwright
automation against a **headful local Chromium** — no docker, no container
rebuild. Use it to iterate fast on selectors and flows (the current pain is the
Instagram comment composer selector).

> Not part of the pnpm workspace. **Do not commit.** Lives under `docs/`.

## How it stays DRY (which worker code it reuses)

It imports the **real** worker source directly (run with `tsx`, which transpiles
TS and rewrites the worker's `.js` ESM specifiers to `.ts`):

- `apps/worker/src/platforms/dom.ts` — `openPost`, `needsLogin`, `likePost`, `commentOnPost`
- `apps/worker/src/sel/index.ts` — `selectorsFor` (the single selector patch point)
- `apps/worker/src/core/browser.ts` — `launchBrowser`, `newAccountContext` (auth context + session injection)
- `apps/worker/src/platforms/{instagram,threads}.ts` — login URL + proof cookies

The **COMMIT** path for like/comment calls the worker's own `likePost` /
`commentOnPost`, so what runs locally is exactly what runs in the container. The
only non-worker logic here is the **dry-run composer probe** in `src/run.ts`,
which walks the same `selectorsFor(...)` candidate list, reports which one
matched, and fills the box **without submitting**.

Why a direct import works with no docker/redis deps: the entire worker import
chain (`dom → auth → browser → session → screenshot → otp → adapter`) imports
`@smm/shared` and `playwright` only as `import type`, which `tsx`/esbuild erase.
Nothing pulls in redis, the queue, or the container bootstrap. `registry.ts`
(the only value-import of `@smm/shared`) is deliberately **not** imported.

## Install

From this directory:

```bash
cd docs/local-worker
npm install                 # installs tsx (+ playwright for types)
```

Playwright Chromium is already installed on this machine
(`~/Library/Caches/ms-playwright`). If it were missing:

```bash
npx playwright install chromium
```

### macOS: the pinned chromium does not launch

The harness pins `playwright@1.48.2`, which resolves **chromium-1140**. On this
machine that build fails to spawn:

```
browserType.launch: spawn Unknown system error -88
```

`src/browser-local.ts` therefore scans `~/Library/Caches/ms-playwright` for
`chromium-*` builds, newest first, and passes a launchable one as
`executablePath` into the worker's own `launchBrowser` — so the worker's real
options still apply and only the binary differs.

Override with `DISPLAY_BROWSER=/path/to/Chrome tsx src/run.ts ...`.

The runtime browser **driver** still comes from `apps/worker/node_modules`
(Playwright 1.48.2); only the launched binary is overridden (see above).

## Commands

Default platform is `instagram`. Default session is
`infra/backup/sessions/session-instagram.json` (relative to the monorepo root).

### login — headful, human logs in + OTP, saves session

```bash
npm run login -- --platform instagram
# or: tsx src/run.ts login --platform instagram
```

Opens the platform login page in a visible browser and waits. Log in (and clear
any OTP/checkpoint) **in the browser**, then press ENTER in the terminal. The
`storageState` is saved back to the session path.

#### YouTube (Google account)

```bash
tsx src/run.ts login  --platform youtube   # saves infra/backup/sessions/session-youtube.json
tsx src/run.ts verify --platform youtube   # loads it, asks youtube.com if it is live
```

Three details make or break this, all verified against the real flow:

1. **Google cookies are not YouTube cookies.** The login URL carries
   `continue=https://www.youtube.com/`; Google sets its session on
   `.google.com`, YouTube only mints its own cookie cluster (`LOGIN_INFO`,
   `SAPISID`, `SID`, `HSID`, `SSID`) once the browser lands on
   `.youtube.com`. Saving a state captured on `accounts.google.com` alone
   produces a file that opens YouTube logged out.
2. **Proof is `LOGIN_INFO` + `SAPISID`, not `__Secure-1PSID`.** The latter is
   set by Google before any YouTube session exists. `verify` goes further and
   reads `ytcfg.get('LOGGED_IN')` off youtube.com — the page's own verdict.
3. **Do not automate the credentials.** Google refuses sign-in from an
   automated browser ("This browser or app may not be secure"). The worker
   already launches headful with `--disable-blink-features=AutomationControlled`
   and a pinned desktop UA; the operator types email/password/2FA. The YouTube
   adapter has no `autofill` — it is deliberate, not missing.

After the session is saved, **close the browser** (the harness does). Cookies
rotate while a signed-in tab stays open, and a rotated session fails on the
next run. Google also invalidates sessions on unusual IP/fingerprint changes:
reuse the same context options (UA, locale `id-ID`, timezone, proxy) the
worker uses — `newAccountContext` already does.

Handing it to `apps/worker`: the file is a standard Playwright `storageState`.
Either import it into an existing account (durable path, works in both pool
modes):

```bash
curl -b jar.txt -X POST localhost:24080/api/accounts/<accountId>/session/import \
  -H 'Content-Type: application/json' \
  --data-binary @infra/backup/sessions/session-youtube.json
```

or, for a legacy docker worker, place it at
`/data/sessions/session-youtube.json` inside the worker's session volume. The
worker loads it into `newAccountContext` per job; a live session means no login
step at all. `verify` is the check that this worked.

### like

```bash
# dry run (default): navigates, locates the like button, does NOT click
tsx src/run.ts like --url https://www.instagram.com/p/XXXX/

# real, irreversible like:
tsx src/run.ts like --url https://www.instagram.com/p/XXXX/ --commit
```

### comment

```bash
# dry run (default): navigates, finds the composer, FILLS it, does NOT submit,
# and prints WHICH candidate selector matched (or that none did)
tsx src/run.ts comment --url https://www.instagram.com/p/XXXX/ --text "OKE"

# real, irreversible comment:
tsx src/run.ts comment --url https://www.instagram.com/p/XXXX/ --text "OKE" --commit
```

### comment-reply — reply to a SPECIFIC comment

The URL is a **comment permalink** (`.../p/<postCode>/c/<commentId>/`), not a post
permalink. Only this form threads the reply under that comment; a bare post URL
is rejected by the API for a reply job.

```bash
tsx src/run.ts comment-reply --url https://www.instagram.com/p/XXXX/c/1789/ --text "setuju" --commit
```

On IG the reply box **pre-fills an `@username` mention**, and the worker keeps it
(that mention is what tags the person). Verified live: the commit result echoes
the rendered text, e.g. `"@rackytampubolon setuju, roadmap dulu baru janji"`.

A reply on IG is **collapsed behind "View replies (N)"** until expanded, so a
later check on the post will not show it without clicking that toggle.

## Flags

| flag | meaning |
| --- | --- |
| `--platform` | `instagram` (default), `threads`, or `youtube` |
| `--url` | target post permalink (required for like/comment) |
| `--text` | comment text (required for comment) |
| `--session` | storageState JSON path (default `infra/backup/sessions/session-<platform>.json`) |
| `--commit` | **actually submit** the like/comment (real, irreversible). Without it: dry run. |
| `--keep-open` | pause before closing the browser (interactive terminal only) |

## Safety

Like/comment against live platforms are **real, irreversible** posts. The tool
defaults to **dry run**: it navigates, fills, and logs what it *would* do, but
never clicks the final submit. It only submits with an explicit `--commit`. Each
run prints whether it ran DRY or COMMIT.

## Fixing the comment composer selector

The `comment` dry run prints the ordered candidate list and exactly which one
matched:

```
[comment] DRY RUN — candidate composer selectors (in order):
           [0] textarea[aria-label*="comment" i]
           [1] div[contenteditable="true"][role="textbox"]
           [2] textarea[placeholder*="comment" i]
[comment] RESULT: matched candidate [1]: div[contenteditable="true"][role="textbox"]
```

or, when the live markup changed:

```
[comment] RESULT: NO composer candidate matched. <-- fix sel/index.ts composerInputs
```

When none match, edit `apps/worker/src/sel/index.ts` (`instagram.composerInputs`),
re-run the dry command, and repeat — no docker rebuild. The fix lands in the real
worker because the selectors are read straight from that file.
