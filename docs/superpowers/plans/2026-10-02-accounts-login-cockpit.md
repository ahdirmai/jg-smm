# Accounts Login Cockpit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consolidate the whole operator login lifecycle (start login, watch the live browser, send the 2FA/CAPTCHA code) into `/accounts` as a per-account detail drawer, replacing `window.prompt` and the scattered modals — so one page carries the entire flow.

**Architecture:** All backend contracts already exist (`POST /api/accounts/{id}/login`, `POST /api/accounts/{id}/input`, the account list with `authStatus`, the container list with `novncUrl`). This plan is web-only: it extracts reusable primitives (`LiveView`, shared badges, platform glyph) out of `live-browser-modal.tsx` and `accounts/page.tsx`, builds a right-anchored detail drawer (a `Dialog`, because `@smm/ui` ships no Sheet), and rewires the accounts table: row click → drawer, an auth-status filter, an action-first sort order, and a clickable "Needs input" stat.

**Tech Stack:** Next.js 15, React client components, shadcn/ui via `@smm/ui` (Radix Dialog), Tailwind, lucide icons. No new dependencies.

**Spec:** Operator requirement agreed 2026-10-02: *"semua prosesnya di /accounts agar mempermudah UI/UX; UI list account bisa diupdate."* Prior research in this session: worker auto-login + CAPTCHA handling (Work package 2 below, deferred).

## Global Constraints

- **No API / OpenAPI / DB changes in work package 1.** Every endpoint used already exists in `apps/web/lib/api.ts` (`startAccountLogin`, `submitAccountInput`, account + container lists).
- The noVNC websocket must not be held while the drawer is closed: the iframe mounts only while the dialog is open AND the live view is shown.
- Existing design system only (DESIGN_SYSTEM §10 / principle 6 as cited throughout `accounts/page.tsx`): tinted pills always carry a glyph + label, platform glyph is a monochrome icon in a soft rounded square, no new palette colors.
- No new dependencies: `@smm/ui` has no Sheet/Drawer — the detail panel is a right-anchored `Dialog` (Radix Dialog is already a dependency).
- Credentials and cookies never appear in the DOM or in logs; the drawer carries only `authStatus`, `username`, `handle`, `lastError` (already in the API response).
- One commit per task. `pnpm typecheck` and `pnpm lint` must pass at the end of each task.

## Review Focus

The failure modes the requirement implies but no type-level check would catch — most dangerous first, each pinned by the browser smoke in Task 5:

- **Row click double-firing.** The table row carries `onClick`; the checkbox and the dropdown must keep their existing `stopPropagation` pattern, and the new drawer must not inherit a second trigger. Clicking the checkbox selects without opening the drawer.
- **Empty code submit.** The 2FA form must not fire `submitAccountInput` with an empty/whitespace-only value; the button is disabled on `code.trim() === ''`.
- **Broken iframe when no live view is published yet.** A freshly created container has no `novncUrl` until its first heartbeat; the drawer must render a hint, not an empty/black iframe box.
- **Stat click target.** "Needs input" must toggle the AUTH filter, not the lifecycle-status filter, and its count stays the fleet-wide NEEDS_INPUT number.
- **Sort mutating the hook's array.** The filtered list is sorted on a copy; `useAccounts()` state must not be reordered in place.

---

### Task 0: Wire `INTERNAL_API_SECRET` to the scaled worker (prerequisite)

**Files:**
- Modify: `compose.yaml` (`worker` service `environment`, beside `PLATFORMS`)

**Interfaces:**
- Produces: none. This is the handshake fix from the session's auto-login research: `.env` arms the API's `/internal` guard (commit `561dd29`), but the fleet-scaled worker never received the secret, so its `claim` / `heartbeat` / `geolocation` calls all return 401 (observed in `smm-worker-*` logs). Dashboard-provisioned containers already get it via the docker provisioner (`apps/api/internal/adapter/dockerprovisioner/docker.go:176-178`); the compose-scaled path was the gap.

- [ ] **Step 1: Add one line to the `worker` service environment**

```yaml
      INTERNAL_API_SECRET: ${INTERNAL_API_SECRET:-}
```

- [ ] **Step 2: Recreate the workers and verify**

```bash
docker compose up -d worker
docker compose logs --tail=40 worker-1 | grep -E 'heartbeat|claim'
```

Expected: no `status 401` on `heartbeat`/`claim` lines (a heartbeat log without the `failed`/`401` marker).

- [ ] **Step 3: Commit**

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

- [ ] **Step 1: Create `live-view.tsx`**

Move `novncClientUrl` (verbatim, doc comment included) and the iframe block: the black `aspect-[16/10]` container, the absolutely-positioned borderless iframe, `allow="clipboard-read; clipboard-write"`. `LiveView` mounts the iframe on mount — its parents (modal/drawer) mount it only while visible, which is what keeps the websocket closed otherwise.

- [ ] **Step 2: Refactor `LiveBrowserModal` to render `<LiveView>` for its body**

Keep: the lazy-load-on-open behaviour (the modal renders the `LiveView` only while `open`), the header with worker name + URL + "Open in tab", the dialog chrome. The modal file must no longer contain the iframe or the URL normalisation.

- [ ] **Step 3: Verify**

```bash
pnpm --filter @smm/web typecheck
```

Expected: PASS; the modal behaves unchanged (no e2e spec covers it — the browser smoke in Task 5 exercises the new drawer instead).

- [ ] **Step 4: Commit**

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

- [ ] **Step 1: Move `statusTone`, `authTone`, `STATUS_ICON`, `AuthBadge`, `StatusBadge` into `account-badges.tsx`** — verbatim, doc comments included.

- [ ] **Step 2: Move `PLATFORM_ICON` + `platformIcon` into `platform-icon.tsx`** — a client component module. Rationale is the existing comment at `accounts/page.tsx:66-72`: the lib stays lucide-free because server components import it; `components/` is client-only, so the glyph moves there, not into the lib.

- [ ] **Step 3: `accounts/page.tsx` imports both from the new files** and deletes the local copies. No markup change.

- [ ] **Step 4: Verify**

```bash
pnpm --filter @smm/web typecheck
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/web/components/account-badges.tsx apps/web/components/platform-icon.tsx 'apps/web/app/(dashboard)/accounts/page.tsx'
git commit -m "refactor(web): hoist account badges and platform glyph to shared components"
```

---

### Task 3: Build the `AccountDetailDrawer`

**Files:**
- Create: `apps/web/components/account-detail-drawer.tsx`

**Interfaces:**
- Consumes: `LiveView` + `novncClientUrl` (Task 1), `AuthBadge`/`StatusBadge` (Task 2), `platformIcon` (Task 2), `Account` from `@/lib/api`.
- Produces:

```tsx
export function AccountDetailDrawer(props: {
  account: Account;                 // the open row; parent guards open on it
  open: boolean;
  onOpenChange: (open: boolean) => void;
  novncUrl: string | null;          // the worker's published live view, or null
  showLiveView: boolean;            // auth in flight, or the operator chose to watch
  canAct: boolean;
  busy: boolean;                    // this account's op in flight (page's `busy === account.id`)
  onStartLogin: (id: string) => void;
  onSubmitInput: (id: string, value: string) => void;
  onToggleWatch: () => void;        // only used by the AUTHENTICATED "Live view" button
}): JSX.Element
```

- [ ] **Step 1: Implement the component**

Right-anchored panel: `DialogContent` with `ml-auto sm:max-w-[520px] sm:rounded-l-2xl` (no Sheet in `@smm/ui`; the dialog overlay dims the table behind it). Content rules, each pinned by the Task 5 smoke:

1. **Header** — platform square (`platformIcon`), `@username`, handle-or-worker-id subtitle, then `AuthBadge` + `StatusBadge`.
2. **Live view block** — `showLiveView && novncUrl` → `<LiveView url={novncUrl} title={...} />` plus an "Open in tab" link built from `novncClientUrl`. `showLiveView && !novncUrl` → the hint "No live view yet — the worker has not published its noVNC port" (never a dead black box).
3. **`authStatus === 'NEEDS_INPUT'`** → the code form replacing `window.prompt`: an `Input` (`inputMode="numeric"`, `maxLength={8}`, autofocused) + a "Send" `Button` disabled unless `code.trim() !== '' && canAct && !busy`. Submit calls `onSubmitInput(account.id, code.trim())` and clears the field. Hint line: "Type the code shown in the live view — or solve the challenge directly in the live view."
4. **`authStatus === 'AUTHENTICATING'`** → status line "Login in progress — the state settles when the worker reports back."
5. **`authStatus === 'AUTHENTICATED'`** → external profile link (`target="_blank"`, `rel="noopener noreferrer"` — reuse `profileUrlFor` from the page by moving it into this file's neighbours: put it in `account-detail-drawer.tsx` and import from there), the `lastVerifiedAt` stamp when present, and a "Live view" ghost button (disabled when `!novncUrl`) that calls `onToggleWatch`.
6. **`authStatus === 'FAILED'`** → `lastError` (nullable) in a destructive-toned block, then the retry affordance below.
7. **Footer** — "Log in" button (`onStartLogin(account.id)`), visible for every state except `QUARANTINED`, disabled while `busy` or `!canAct`.

No credentials, cookies or secret material in the DOM (Global Constraints).

- [ ] **Step 2: Verify**

```bash
pnpm --filter @smm/web typecheck
```

Expected: PASS. (No web unit-test harness exists; the behavioural gate is the Task 5 browser smoke.)

- [ ] **Step 3: Commit**

```bash
git add apps/web/components/account-detail-drawer.tsx
git commit -m "feat(web): per-account detail drawer with live view and code input"
```

---

### Task 4: Rewire the accounts page into the cockpit

**Files:**
- Modify: `apps/web/app/(dashboard)/accounts/page.tsx`

**Interfaces:**
- Consumes: `AccountDetailDrawer` (Task 3), `AuthBadge`/`StatusBadge`/`platformIcon` (Task 2), `profileUrlFor` (now in Task 3's file).
- Produces: the `/accounts` page as the single login surface.

- [ ] **Step 1: Swap the modal state for the drawer state**

Replace `const [live, setLive] = useState<{ name; url } | null>(null)` with:

```tsx
const [detail, setDetail] = useState<{ account: Account; watch: boolean } | null>(null);
```

Render `<AccountDetailDrawer>` where `<LiveBrowserModal>` was; drop the `LiveBrowserModal` import.

- [ ] **Step 2: Row click opens the drawer; drop `openAccount`**

`onClick={() => setDetail({ account: a, watch: needsLiveView(a) })}`. Delete `openAccount` (profile opening now lives in the row's icon link and inside the drawer). Delete the standalone `MonitorPlay` icon button on the row — the row click covers it; keep the `ExternalLink` profile button and the dropdown untouched except the changes below.

- [ ] **Step 3: Reroute the dropdown items**

"Enter code" → `setDetail({ account: a, watch: true })` (the form is in the drawer; delete the `window.prompt`-based `submitInput` body, keep a page-level `submitInput(id, value)` wrapper around `api.submitAccountInput` + `refresh`). "Live view" (authenticated) → `setDetail({ account: a, watch: true })`.

- [ ] **Step 4: Add the auth-status filter**

A third `Select` beside platform/status with values `all` + the `authStatus` enum (`AUTHENTICATING`, `NEEDS_INPUT`, `AUTHENTICATED`, `FAILED`, `QUARANTINED`); extend the `filtered` memo with `if (auth !== 'all' && a.authStatus !== auth) return false;`.

- [ ] **Step 5: Action-first sort (on a copy)**

```tsx
const AUTH_RANK: Record<Account['authStatus'], number> = {
  AUTHENTICATING: 0, NEEDS_INPUT: 0, FAILED: 1, QUARANTINED: 2, AUTHENTICATED: 3,
};
// in `filtered`: .slice().sort((a, b) =>
//   AUTH_RANK[a.authStatus] - AUTH_RANK[b.authStatus]
//   || a.platform.localeCompare(b.platform)
//   || a.username.localeCompare(b.username));
```

- [ ] **Step 6: Make "Needs input" actionable**

`Stat` gains an optional `onSelect?: () => void` + `active?: boolean`; the "Needs input" card becomes a `Button`-style clickable card that toggles the auth filter between `NEEDS_INPUT` and `'all'` (active styling when the filter is on). The number stays the fleet-wide count.

- [ ] **Step 7: Copy fix** — "Nothing matches" hint: "Clear the filters to see the rest of the fleet."

- [ ] **Step 8: Verify**

```bash
pnpm --filter @smm/web typecheck && pnpm --filter @smm/web lint
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add 'apps/web/app/(dashboard)/accounts/page.tsx'
git commit -m "feat(web): /accounts is now the login cockpit (drawer, auth filter, action-first sort)"
```

---

### Task 5: End-to-end verification on the live stack

**Files:** none (verification + plan checkboxes).

- [ ] **Step 1: Workspace checks**

```bash
pnpm typecheck && pnpm lint
```

- [ ] **Step 2: Rebuild and restart the web service**

```bash
docker compose build web && docker compose up -d web
curl -I http://localhost:24081 | head -1   # expect HTTP/1.1 200 OK
```

- [ ] **Step 3: Browser smoke (the Review Focus list, executed in order)**

1. Log in at `http://localhost:24081/login` with the seeded owner (`owner@smm.local` / the `.env` seed password) — the dashboard requires a session.
2. Create a throwaway account on `/accounts/new` bound to a running worker (any platform; `instagram` is the default).
3. Row click opens the right-anchored drawer; the checkbox click does NOT open it (Review Focus 1).
4. With the new row (no heartbeat yet), the drawer shows the "No live view yet" hint, not a dead box (Review Focus 3).
5. Click "Log in" in the drawer footer → `authStatus` moves to `AUTHENTICATING`; the drawer's live view streams the worker's browser on the platform login page; the table row stays in place with the AUTHENTICATING badge.
6. Auth filter: select `NEEDS_INPUT` → only those rows show; the "Needs input" stat reflects it (toggle on/off works, Review Focus 4).
7. If the row reaches `NEEDS_INPUT` (OTP wall), the code form is present; the "Send" button is disabled on empty input (Review Focus 2). (Not guaranteed on a live platform — the form's disabled state is asserted in the AUTHENTICATING view's sibling state or with a parked login.)
8. No `window.prompt` anywhere on the page; no console errors on the page.
9. Cleanup: delete the throwaway account via the row dropdown.

- [ ] **Step 4: Tick the checkboxes in this plan and commit**

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
