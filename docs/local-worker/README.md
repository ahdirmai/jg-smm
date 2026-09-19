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

The runtime browser driver is resolved from `apps/worker/node_modules`
(Playwright 1.48.2), so the headful browser matches the worker's version.

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

## Flags

| flag | meaning |
| --- | --- |
| `--platform` | `instagram` (default) or `threads` |
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
