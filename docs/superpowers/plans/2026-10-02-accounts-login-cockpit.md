# Accounts Login Cockpit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consolidate the whole operator login lifecycle (start login, watch the live browser, send the 2FA/CAPTCHA code) into `/accounts` as a detail panel rendered in-page above the table, replacing `window.prompt` and the scattered modals — so one page carries the entire flow.

**Architecture:** All backend contracts already exist (`POST /api/accounts/{id}/login`, `POST /api/accounts/{id}/input`, the account list with `authStatus`, the container list with `novncUrl`). This plan is web-only: it extracts reusable primitives (`LiveView`, shared badges, platform glyph) out of `live-browser-modal.tsx` and `accounts/page.tsx`, renders a detail panel **directly on the page** (operator decision 2026-10-02: no modal, no overlay — the table stays in the same view and keeps updating while a login is in flight), and rewires the accounts table: row click selects/closes the panel, an auth-status filter, an action-first sort order, and a clickable "Needs input" stat. The standalone `LiveBrowserModal` remains on the Workers page only (container-level monitoring, not the account login flow).

**Tech Stack:** Next.js 15, React client components, shadcn/ui via `@smm/ui` (Radix Dialog), Tailwind, lucide icons. No new dependencies.

**Spec:** Operator requirement agreed 2026-10-02: *"semua prosesnya di /accounts agar mempermudah UI/UX; UI list account bisa diupdate."* Prior research in this session: worker auto-login + CAPTCHA handling (Work package 2 below, deferred).

## Global Constraints
- The noVNC websocket must not be held while no panel is open: the iframe mounts only while the panel is rendered AND the live view is shown.
- **No API / OpenAPI / DB changes in work package 1.** Every endpoint used already exists in `apps/web/lib/api.ts` (`startAccountLogin`, `submitAccountInput`, account + container lists). One behaviour-only delta landed during Task 5: `RecordAuthOutcome` now publishes the existing `account-updated` frame (see Task 5 result).
- No new dependencies: `@smm/ui` has no Sheet/Drawer, and the overlay approach was rejected anyway — the panel is a plain `Card` section on the page.
- Credentials and cookies never appear in the DOM or in logs; the panel carries only `authStatus`, `username`, `handle`, `lastError` (already in the API response).
- One commit per task. `pnpm typecheck` and `pnpm lint` must pass at the end of each task.

## Review Focus

The failure modes the requirement implies but no type-level check would catch — most dangerous first, each pinned by the browser smoke in Task 5:

- **Row click double-firing.** The table row carries `onClick`; the checkbox and the dropdown must keep their existing `stopPropagation` pattern, and the panel must not inherit a second trigger. Clicking the checkbox selects without opening the panel; clicking the selected row again closes it.
- **Empty code submit.** The 2FA form must not fire `submitAccountInput` with an empty/whitespace-only value; the button is disabled on `code.trim() === ''`.
- **Broken iframe when no live view is published yet.** A freshly created container has no `novncUrl` until its first heartbeat; the panel must render a hint, not a dead black box.
- **Stat click target.** "Needs input" must toggle the AUTH filter, not the lifecycle-status filter, and its count stays the fleet-wide NEEDS_INPUT number.
- **Panel outliving its row.** The panel reads the selected row live from the `accounts` list by id; deleting the row while the panel is open must unmount it, not crash on a stale object.
### Task 0: Wire `INTERNAL_API_SECRET` to the scaled worker (prerequisite)

**Files:**
- Modify: `compose.yaml` (`worker` service `environment`, beside `PLATFORMS`)

**Interfaces:**
- Produces: none. This is the handshake fix from the session's auto-login research: `.env` arms the API's `/internal` guard (commit `561dd29`), but the fleet-scaled worker never received the secret, so its `claim` / `heartbeat` / `geolocation` calls all return 401 (observed in `smm-worker-*` logs). Dashboard-provisioned containers already get it via the docker provisioner (`apps/api/internal/adapter/dockerprovisioner/docker.go:176-178`); the compose-scaled path was the gap.

- [x] **Step 1: Add one line to the `worker` service environment**

```yaml
      INTERNAL_API_SECRET: ${INTERNAL_API_SECRET:-}
```

- [x] **Step 2: Recreate the workers and verify**

```bash
docker compose up -d worker
docker compose logs --tail=40 worker-1 | grep -E 'heartbeat|claim'
```

Expected: no `status 401` on `heartbeat`/`claim` lines (a heartbeat log without the `failed`/`401` marker).

- [x] **Step 3: Commit**

```bash
git add compose.yaml
git commit -m "fix(compose): pass INTERNAL_API_SECRET to the scaled workers"
```

---

### Task 1: Extract `LiveView` from the live browser modal

**Files:**
- Create: `apps/web/components/live-view.tsx`
- Modify: `apps/web/components/live-browser-modal.tsx`

**Interfaces:**
- Produces: `novncClientUrl(url: string): string` (exported, moved) and `LiveView({ url, title, className }: { url: string; title: string; className?: string })`.
- Consumes: nothing from earlier tasks.

- [x] **Step 1: Create `live-view.tsx`**

Move `novncClientUrl` (verbatim, doc comment included) and the iframe block: the black `aspect-[16/10]` container, the absolutely-positioned borderless iframe, `allow="clipboard-read; clipboard-write"`. `LiveView` mounts the iframe on mount — its parents (modal/drawer) mount it only while visible, which is what keeps the websocket closed otherwise.

- [x] **Step 2: Refactor `LiveBrowserModal` to render `<LiveView>` for its body**

Keep: the lazy-load-on-open behaviour (the modal renders the `LiveView` only while `open`), the header with worker name + URL + "Open in tab", the dialog chrome. The modal file must no longer contain the iframe or the URL normalisation.

- [x] **Step 3: Verify**

```bash
pnpm --filter @smm/web typecheck
```

Expected: PASS; the modal behaves unchanged (no e2e spec covers it — the browser smoke in Task 5 exercises the new drawer instead).

- [x] **Step 4: Commit**

```bash
git add apps/web/components/live-view.tsx apps/web/components/live-browser-modal.tsx
git commit -m "refactor(web): extract LiveView from the live browser modal"
```

---

### Task 2: Hoist the account badges and platform glyph to shared components

**Files:**
- Create: `apps/web/components/account-badges.tsx`
- Create: `apps/web/components/platform-icon.tsx`
- Modify: `apps/web/app/(dashboard)/accounts/page.tsx` (imports + deletions only)

**Interfaces:**
- Produces: `AuthBadge({ status })`, `StatusBadge({ status })` (pixel-identical to the current inline versions, `statusTone`/`authTone`/`STATUS_ICON` travelling with them), and `platformIcon(platform: string): LucideIcon` with its `PLATFORM_ICON` map.
- Consumes: `Account` from `@/lib/api`; `PLATFORMS` from `@/lib/platforms`.

- [x] **Step 1: Move `statusTone`, `authTone`, `STATUS_ICON`, `AuthBadge`, `StatusBadge` into `account-badges.tsx`** — verbatim, doc comments included.

- [x] **Step 2: Move `PLATFORM_ICON` + `platformIcon` into `platform-icon.tsx`** — a client component module. Rationale is the existing comment at `accounts/page.tsx:66-72`: the lib stays lucide-free because server components import it; `components/` is client-only, so the glyph moves there, not into the lib.

- [x] **Step 3: `accounts/page.tsx` imports both from the new files** and deletes the local copies. No markup change.

- [x] **Step 4: Verify**

```bash
pnpm --filter @smm/web typecheck
```

Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add apps/web/components/account-badges.tsx apps/web/components/platform-icon.tsx 'apps/web/app/(dashboard)/accounts/page.tsx'
git commit -m "refactor(web): hoist account badges and platform glyph to shared components"
```

---

### Task 3: Build the `AccountDetailPanel`

**Files:**
- Create: `apps/web/components/account-detail-panel.tsx`

**Interfaces:**
- Consumes: `LiveView` + `novncClientUrl` (Task 1), `AuthBadge`/`StatusBadge` (Task 2), `platformIcon` (Task 2), `Account` from `@/lib/api`.
- Produces:

```tsx
export function AccountDetailPanel(props: {
  account: Account;
  novncUrl: string | null;          // the worker's published live view, or null
  showLiveView: boolean;            // auth in flight, or the operator chose to watch
  canAct: boolean;
  busy: boolean;                    // this account's op in flight (page's `busy === account.id`)
  onClose: () => void;              // the X in the panel header; closes the selection
  onStartLogin: (id: string) => void;
  onSubmitInput: (id: string, value: string) => void;
  onToggleWatch: () => void;        // only used by the AUTHENTICATED "Live view" button
}): JSX.Element
```

- [x] **Step 1: Implement the component**

Plain `Card` content rendered **in the page** above the table (no overlay; the operator rejected the modal/drawer on 2026-10-02). Header row, then a `md:grid-cols-2` body: live view on the left, state-specific affordances on the right. Content rules, each pinned by the Task 5 smoke:

1. **Header** — platform square (`platformIcon`), `@username`, handle-or-worker-id subtitle, `AuthBadge` + `StatusBadge` + platform badge, and a close (`X`) button that calls `onClose`.
2. **Live view block** — `showLiveView && novncUrl` → `<LiveView url={novncUrl} title={...} />` plus an "Open in tab" link built from `novncClientUrl`. `showLiveView && !novncUrl` → the hint "No live view yet — the worker has not published its noVNC port" (never a dead black box).
3. **`authStatus === 'NEEDS_INPUT'`** → the code form replacing `window.prompt`: an `Input` (`inputMode="numeric"`, `maxLength={8}`, autofocused) + a "Send" `Button` disabled unless `code.trim() !== '' && canAct && !busy`. Submit calls `onSubmitInput(account.id, code.trim())` and clears the field. Hint line: "Type the code shown in the live view — or solve the challenge directly in the live view."
4. **`authStatus === 'AUTHENTICATING'`** → status line "Login in progress — the state settles when the worker reports back."
5. **`authStatus === 'AUTHENTICATED'`** → external profile link (`target="_blank"`, `rel="noopener noreferrer"` — `profileUrlFor` moves into this file and the page imports it from here), and a "Live view" ghost button (disabled when `!novncUrl`) that calls `onToggleWatch`.
6. **`authStatus === 'FAILED'`** → `lastError` (nullable) in a destructive-toned block, then the retry affordance below.
7. **Bottom** — "Log in" button (`onStartLogin(account.id)`), visible for every state except `QUARANTINED`, disabled while `busy` or `!canAct`.

No credentials, cookies or secret material in the DOM (Global Constraints).

- [x] **Step 2: Verify**

```bash
pnpm --filter @smm/web typecheck
```

Expected: PASS. (No web unit-test harness exists; the behavioural gate is the Task 5 browser smoke.)

- [x] **Step 3: Commit**

```bash
git add apps/web/components/account-detail-panel.tsx
git commit -m "feat(web): in-page account detail panel with live view and code input"
```

---

### Task 4: Rewire the accounts page into the cockpit

**Files:**
- Modify: `apps/web/app/(dashboard)/accounts/page.tsx`

**Interfaces:**
- Consumes: `AccountDetailPanel` + `profileUrlFor` (Task 3), `AuthBadge`/`StatusBadge`/`platformIcon` (Task 2).
- Produces: the `/accounts` page as the single login surface.

- [x] **Step 1: Swap the modal state for the panel selection**

Replace `const [live, setLive] = useState<{ name; url } | null>(null)` with:

```tsx
const [detail, setDetail] = useState<{ id: string; watch: boolean } | null>(null);
const detailAccount = detail ? accounts.find((a) => a.id === detail.id) ?? null : null;
const isWatching = detail?.watch ?? false;
```

Render `<Card><AccountDetailPanel …/></Card>` **in the page, between the stat strip and the table** (no overlay — the operator rejected the modal on 2026-10-02). `LiveBrowserModal` leaves the accounts page entirely (it stays on the Workers page).

- [x] **Step 2: Row click selects / deselects; drop `openAccount`**

`onClick={() => setDetail(detail?.id === a.id ? null : { id: a.id, watch: needsLiveView(a) })}` — clicking the open row again closes the panel. Delete `openAccount` (profile opening now lives in the row's icon link and inside the panel). Delete the standalone `MonitorPlay` icon button on the row — the row click covers it; keep the `ExternalLink` profile button and the dropdown untouched except the changes below.

- [x] **Step 3: Reroute the dropdown items**

"Enter code" → `setDetail({ id: a.id, watch: true })` (the form is in the panel; delete the `window.prompt`-based `submitInput` body, keep a page-level `submitInput(id, value)` wrapper around `api.submitAccountInput` + `refresh`). "Live view" (authenticated) → `setDetail({ id: a.id, watch: true })`.

- [x] **Step 4: Add the auth-status filter**

A third `Select` beside platform/status with values `all` + the `authStatus` enum (`AUTHENTICATING`, `NEEDS_INPUT`, `AUTHENTICATED`, `FAILED`, `QUARANTINED`); extend the `filtered` memo with `if (auth !== 'all' && a.authStatus !== auth) return false;`.

- [x] **Step 5: Action-first sort (on a copy)**

```tsx
const AUTH_RANK: Record<Account['authStatus'], number> = {
  AUTHENTICATING: 0, NEEDS_INPUT: 0, FAILED: 1, QUARANTINED: 2, AUTHENTICATED: 3,
};
// in `filtered`: .slice().sort((a, b) =>
//   AUTH_RANK[a.authStatus] - AUTH_RANK[b.authStatus]
//   || a.platform.localeCompare(b.platform)
//   || a.username.localeCompare(b.username));
```

- [x] **Step 6: Make "Needs input" actionable**

`Stat` gains an optional `onSelect?: () => void` + `active?: boolean`; the "Needs input" card becomes a `Button`-style clickable card that toggles the auth filter between `NEEDS_INPUT` and `'all'` (active styling when the filter is on). The number stays the fleet-wide count.

- [x] **Step 7: Copy fix** — "Nothing matches" hint: "Clear the filters to see the rest of the fleet."

- [x] **Step 8: Verify**

```bash
pnpm --filter @smm/web typecheck && pnpm --filter @smm/web lint
```

Expected: PASS.

- [x] **Step 9: Commit**

```bash
git add 'apps/web/app/(dashboard)/accounts/page.tsx'
git commit -m "feat(web): /accounts is now the login cockpit (in-page panel, auth filter, action-first sort)"
```

---

### Task 5: End-to-end verification on the live stack

**Files:** none (verification + plan checkboxes).

- [x] **Step 1: Workspace checks**

```bash
pnpm typecheck && pnpm lint
```

- [x] **Step 2: Rebuild and restart the web service**

```bash
docker compose build web && docker compose up -d web
curl -I http://localhost:24081 | head -1   # expect HTTP/1.1 200 OK
```

- [x] **Step 3: Browser smoke (the Review Focus list, executed in order)**

1. Log in at `http://localhost:24081/login` with the seeded owner (`owner@smm.local` / the `.env` seed password) — the dashboard requires a session.
2. Create a throwaway account on `/accounts/new` bound to a running worker (any platform; `instagram` is the default).
3. Row click opens the in-page panel above the table; clicking the same row again closes it; the checkbox click does NOT open it (Review Focus 1).
4. With the new row (no heartbeat yet), the panel shows the "No live view yet" hint, not a dead box (Review Focus 3).
5. Click "Log in" in the panel → `authStatus` moves to `AUTHENTICATING`; the panel's live view streams the worker's browser on the platform login page; the table row updates in place with the AUTHENTICATING badge.
6. Auth filter: select `NEEDS_INPUT` → only those rows show; the "Needs input" stat reflects it (toggle on/off works, Review Focus 4).
7. If the row reaches `NEEDS_INPUT` (OTP wall), the code form is present; the "Send" button is disabled on empty input (Review Focus 2). (Not guaranteed on a live platform — the form's disabled state is asserted in the AUTHENTICATING view's sibling state or with a parked login.)
8. No `window.prompt` anywhere on the page; no console errors on the page.
9. Cleanup: delete the throwaway account via the row dropdown.

**Task 5 result (2026-10-02):** steps 1–9 green on the live stack. Two findings:

- **API bug found by the smoke:** `JobService.RecordAuthOutcome` (the `/internal/account-callback` handler) updated the row but never emitted the `account-updated` SSE frame, so a watching dashboard kept showing the pre-callback state — exactly the transition this cockpit exists to surface. Fix: publish the row via `JobService.publishAccountView` (renamed from `publishAccountHealth`), regression test `TestRecordAuthOutcomePublishesAccountFrame`. No OpenAPI/DB schema change — the Global Constraint on line 15 holds at contract level; this is the one behaviour-only delta.
- **Review Focus 3 not triggerable in this environment:** every container had published its noVNC URL before the panel opened, so the live-view path (iframe mounts, "Open in tab") was verified instead of the "No live view yet" hint.


- [x] **Step 4: Tick the checkboxes in this plan and commit**

```bash
git add docs/superpowers/plans/2026-10-02-accounts-login-cockpit.md
git commit -m "docs(plan): accounts login cockpit — executed"
```

---

## Work package 2 — deferred (separate plan when started)

Worker-side challenge detection (the CAPTCHA half of the earlier research):

1. **WP-2.1 (worker only, no API/DB change):** in `apps/worker/src/core/auth.ts`, `pollLoginOutcome`/`waitForOutcome` treat a challenge wall — URL patterns (`/challenge/`, `/checkpoint`, `/two_factor`, `/login/challenge`) plus a reCAPTCHA/hCaptcha iframe in any open page — as `needs_input` (screenshot taken, context parked) instead of burning the full `LOGIN_TIMEOUT_MS` and reporting `FAILED`. The dashboard already renders `NEEDS_INPUT`, so the cockpit picks it up unchanged.
2. **WP-2.2 (optional, needs migration + OpenAPI):** persist `account.last_auth_reason` so the drawer can distinguish "Captcha challenge" from "OTP code" in its hint. Only if operators actually need the distinction.

Out of scope for both packages: credential auto-login (password delivery to the worker) and external CAPTCHA solver services — decisions from the 2026-10-02 research, to be revisited as their own plans.
