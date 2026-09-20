/**
 * LOCAL dev harness for the SMM worker's Playwright automation.
 *
 * Purpose: run the worker's real login / like / comment flows against a HEADFUL
 * local Chromium so selectors (especially the IG comment composer) can be fixed
 * and iterated with ZERO docker rebuild.
 *
 * DRY: this file imports the *real* worker automation directly (run via tsx,
 * which transpiles TS and rewrites the worker's `.js` ESM specifiers to `.ts`):
 *   - like/comment (COMMIT path) call the worker's own `likePost` / `commentOnPost`
 *   - selectors come from the worker's `selectorsFor` (the single patch point)
 *   - the browser + authenticated context come from the worker's `browser.ts`
 * The whole worker import chain (dom → auth → browser → session → screenshot →
 * otp → adapter) imports `@smm/shared` and `playwright` only as `import type`,
 * so nothing docker/redis-only is pulled in at runtime. See README.md.
 *
 * The only NON-worker logic here is the DRY-RUN probe: it walks the same
 * selector candidates and FILLS the composer but never submits, and it prints
 * which candidate matched. That is the signal for fixing the composer selector.
 */
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { createInterface } from 'node:readline/promises';
import { stdin, stdout } from 'node:process';

import type { BrowserContext, Locator, Page } from 'playwright';

// --- REAL worker automation (imported directly, DRY) ------------------------
import {
  openPost,
  needsLogin,
  likePost,
  commentOnPost,
  replyToComment,
} from '../../../apps/worker/src/platforms/dom.js';
import { selectorsFor, type SelectorSet } from '../../../apps/worker/src/sel/index.js';
import { launchBrowser, newAccountContext } from '../../../apps/worker/src/core/browser.js';
import { instagramAdapter } from '../../../apps/worker/src/platforms/instagram.js';
import { threadsAdapter } from '../../../apps/worker/src/platforms/threads.js';
import type { PlatformAdapter } from '../../../apps/worker/src/platforms/adapter.js';
import type { ActionJob } from '../../../apps/worker/src/types.js';

// ----------------------------------------------------------------------------

const MONOREPO_ROOT = resolve(import.meta.dirname, '../../..');
const DEFAULT_SESSION = 'infra/backup/sessions/session-instagram.json';

const ADAPTERS: Record<string, PlatformAdapter> = {
  instagram: instagramAdapter,
  threads: threadsAdapter,
};

interface Args {
  cmd: string;
  platform: string;
  url?: string;
  text?: string;
  session: string;
  commit: boolean;
  keepOpen: boolean;
}

function parseArgs(argv: string[]): Args {
  const cmd = argv[0] ?? '';
  const flags = argv.slice(1);
  const get = (name: string): string | undefined => {
    const i = flags.indexOf(`--${name}`);
    return i >= 0 ? flags[i + 1] : undefined;
  };
  const has = (name: string): boolean => flags.includes(`--${name}`);
  const platform = get('platform') ?? 'instagram';
  return {
    cmd,
    platform,
    url: get('url'),
    text: get('text'),
    session: get('session') ?? defaultSessionFor(platform),
    commit: has('commit'),
    keepOpen: has('keep-open'),
  };
}

/** Default session path tracks the platform (IG default matches the task). */
function defaultSessionFor(platform: string): string {
  if (platform === 'instagram') return DEFAULT_SESSION;
  return `infra/backup/sessions/session-${platform}.json`;
}

/** Resolve a possibly-relative session path against the monorepo root. */
function sessionPath(p: string): string {
  return resolve(MONOREPO_ROOT, p);
}

function adapterFor(platform: string): PlatformAdapter {
  const a = ADAPTERS[platform];
  if (!a) {
    fail(`unknown platform "${platform}" (supported: ${Object.keys(ADAPTERS).join(', ')})`);
  }
  return a;
}

function log(msg: string): void {
  stdout.write(`${msg}\n`);
}

function fail(msg: string): never {
  stdout.write(`\n[harness] ERROR: ${msg}\n`);
  process.exit(1);
}

async function loadStorageState(path: string): Promise<unknown | undefined> {
  try {
    const raw = await readFile(path, 'utf8');
    return JSON.parse(raw);
  } catch {
    return undefined;
  }
}

/** Report whether the loaded session carries the platform's proof cookies. */
function reportSession(state: unknown, adapter: PlatformAdapter, path: string): void {
  if (!state) {
    log(`[session] none found at ${path} (running unauthenticated)`);
    return;
  }
  const cookies = ((state as { cookies?: { name: string }[] }).cookies ?? []).map((c) => c.name);
  const have = new Set(cookies);
  const missing = adapter.sessionCookies.filter((n) => !have.has(n));
  if (missing.length === 0) {
    log(`[session] loaded ${path} — proof cookies present (${adapter.sessionCookies.join(', ')})`);
  } else {
    log(`[session] loaded ${path} — WARNING missing proof cookies: ${missing.join(', ')}`);
  }
}

async function pressEnter(prompt: string): Promise<void> {
  const rl = createInterface({ input: stdin, output: stdout });
  await rl.question(prompt);
  rl.close();
}

/** Build the minimal ActionJob the worker DOM helpers read (id + targetUrl). */
function makeJob(platform: string, url: string, action: string, text?: string): ActionJob {
  return {
    id: `local-${Date.now()}`,
    accountId: 'local',
    platform: platform as ActionJob['platform'],
    action,
    targetUrl: url,
    attempt: 1,
    ...(text ? { text } : {}),
  };
}

// --- DRY-RUN composer probe (the selector-iteration signal) -----------------

/** Ordered composer candidates, mirroring dom.ts `composerCandidates`. */
function composerCandidates(sel: SelectorSet): string[] {
  if (sel.composerInputs && sel.composerInputs.length > 0) return sel.composerInputs;
  return sel.composerInput ? [sel.composerInput] : [];
}

/**
 * Walk the candidate selectors IN ORDER and return the first present one, with
 * its index — same presence-not-waitFor strategy dom.ts uses, so a candidate
 * that never mounts is skipped instantly instead of eating a timeout.
 */
async function probeComposer(
  page: Page,
  candidates: string[],
): Promise<{ selector: string; index: number; loc: Locator } | undefined> {
  for (let i = 0; i < candidates.length; i++) {
    const loc = page.locator(candidates[i]!).first();
    const present = await loc
      .count()
      .then((n) => n > 0)
      .catch(() => false);
    if (present) return { selector: candidates[i]!, index: i, loc };
  }
  return undefined;
}

// --- commands ---------------------------------------------------------------

/** Auto-save poll bound: how long to wait for the operator to finish logging in. */
const LOGIN_POLL_TIMEOUT_MS = 10 * 60_000;
const LOGIN_POLL_MS = 2_000;
/** Once proof cookies appear, wait this long so late cookies (rur, csrftoken) settle. */
const LOGIN_SETTLE_MS = 2_500;

/**
 * Poll the live context until the adapter's proof cookies are all present, or
 * the bound expires. This is what makes login work when the harness is
 * backgrounded (no TTY to press ENTER against): the session is detected and
 * saved automatically the moment the operator finishes logging in.
 */
async function waitForProofCookies(
  ctx: BrowserContext,
  proof: readonly string[],
  timeoutMs: number,
): Promise<boolean> {
  const started = Date.now();
  for (;;) {
    const have = new Set((await ctx.cookies()).map((c) => c.name));
    if (proof.every((n) => have.has(n))) return true;
    if (Date.now() - started >= timeoutMs) return false;
    await sleep(LOGIN_POLL_MS);
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

async function cmdLogin(args: Args): Promise<void> {
  const adapter = adapterFor(args.platform);
  const outPath = sessionPath(args.session);
  log(`[login] platform=${args.platform} headful`);
  log(`[login] will save session to ${outPath}`);

  const { browser, close } = await launchBrowser();
  // Fresh context (no storageState) for a clean operator login.
  const ctx = await newAccountContext(browser, args.platform as ActionJob['platform']);
  const page = await ctx.newPage();
  await page.goto(adapter.loginUrl, { waitUntil: 'domcontentloaded' });
  log(`[login] opened ${adapter.loginUrl}`);
  log('[login] Log in (and complete OTP/checkpoint) IN THE BROWSER window now.');
  log(`[login] Auto-saves as soon as proof cookies (${adapter.sessionCookies.join(', ')}) appear.`);
  log(`[login] No ENTER needed (${Math.round(LOGIN_POLL_TIMEOUT_MS / 60_000)} min budget). On a TTY, ENTER saves immediately.`);

  // Race the cookie poll against an optional manual ENTER. On a backgrounded
  // run stdin is detached, so `pressEnter` never resolves and the cookie poll
  // is the only trigger — which is exactly what we want for automation.
  const cookiePoll = waitForProofCookies(ctx, adapter.sessionCookies, LOGIN_POLL_TIMEOUT_MS).then(
    (ok) => (ok ? 'cookies' : 'timeout'),
  );
  const manual = stdin.isTTY
    ? pressEnter('[login] ...or press ENTER here once logged in to save now. ').then(() => 'enter')
    : new Promise<string>(() => {}); // never resolves off-TTY
  const trigger = await Promise.race([cookiePoll, manual]);

  if (trigger === 'cookies') {
    log('[login] proof cookies detected — letting late cookies settle...');
    await sleep(LOGIN_SETTLE_MS);
  } else if (trigger === 'timeout') {
    log(`[login] WARNING proof-cookie timeout after ${Math.round(LOGIN_POLL_TIMEOUT_MS / 60_000)} min — saving whatever is present.`);
  }

  const state = await ctx.storageState();
  const cookieNames = new Set(state.cookies.map((c) => c.name));
  const missing = adapter.sessionCookies.filter((n) => !cookieNames.has(n));
  if (missing.length > 0) {
    log(`[login] WARNING proof cookies still missing: ${missing.join(', ')} — saving anyway.`);
  } else {
    log(`[login] proof cookies present: ${adapter.sessionCookies.join(', ')}`);
  }
  await mkdir(dirname(outPath), { recursive: true });
  await writeFile(outPath, JSON.stringify(state), 'utf8');
  log(`[login] saved session -> ${outPath}`);
  await close();
}

async function cmdLike(args: Args): Promise<void> {
  if (!args.url) fail('like requires --url <postUrl>');
  const adapter = adapterFor(args.platform);
  const sel = selectorsFor(args.platform as ActionJob['platform']);
  const state = await loadStorageState(sessionPath(args.session));
  reportSession(state, adapter, sessionPath(args.session));

  const { browser, close } = await launchBrowser();
  const ctx = await newAccountContext(browser, args.platform as ActionJob['platform'], state);
  const page = await ctx.newPage();
  try {
    await openPost(page, args.url!);
    log(`[like] opened ${page.url()}`);
    if (await needsLogin(page)) {
      log('[like] AUTH WALL — session appears dead (redirected to login/checkpoint).');
      await maybeKeepOpen(args, ctx);
      return;
    }

    if (!args.commit) {
      // DRY: locate the like affordance, report, do NOT click.
      const found = sel.likeButton
        ? await page
            .locator(sel.likeButton)
            .first()
            .count()
            .then((n) => n > 0)
            .catch(() => false)
        : false;
      log(
        found
          ? `[like] DRY RUN — like button located via: ${sel.likeButton} (NOT clicked)`
          : `[like] DRY RUN — like button NOT FOUND via: ${sel.likeButton}`,
      );
      log('[like] dry run complete (no like submitted).');
    } else {
      log('[like] COMMIT — running the real worker likePost() (this is a REAL like)...');
      const res = await likePost({ page, sel, job: makeJob(args.platform, args.url!, 'like') });
      log(`[like] COMMIT result: ${JSON.stringify(res)}`);
    }
    await maybeKeepOpen(args, ctx);
  } finally {
    await close();
  }
}

async function cmdComment(args: Args): Promise<void> {
  if (!args.url) fail('comment requires --url <postUrl>');
  if (!args.text) fail('comment requires --text "..."');
  const adapter = adapterFor(args.platform);
  const sel = selectorsFor(args.platform as ActionJob['platform']);
  const state = await loadStorageState(sessionPath(args.session));
  reportSession(state, adapter, sessionPath(args.session));

  const { browser, close } = await launchBrowser();
  const ctx = await newAccountContext(browser, args.platform as ActionJob['platform'], state);
  const page = await ctx.newPage();
  try {
    await openPost(page, args.url!);
    log(`[comment] opened ${page.url()}`);
    if (await needsLogin(page)) {
      log('[comment] AUTH WALL — session appears dead (redirected to login/checkpoint).');
      await maybeKeepOpen(args, ctx);
      return;
    }

    if (!args.commit) {
      await commentDryRun(page, sel, args.text!);
    } else {
      log('[comment] COMMIT — running the real worker commentOnPost() (this POSTS a REAL comment)...');
      const res = await commentOnPost(
        { page, sel, job: makeJob(args.platform, args.url!, 'comment', args.text) },
        args.text!,
      );
      log(`[comment] COMMIT result: ${JSON.stringify(res)}`);
    }
    await maybeKeepOpen(args, ctx);
  } finally {
    await close();
  }
}

async function cmdCommentReply(args: Args): Promise<void> {
  if (!args.url) fail('comment-reply requires --url <commentPermalink>  (e.g. .../p/<post>/c/<commentId>/)');
  if (!args.text) fail('comment-reply requires --text "..."');
  const adapter = adapterFor(args.platform);
  const sel = selectorsFor(args.platform as ActionJob['platform']);
  const state = await loadStorageState(sessionPath(args.session));
  reportSession(state, adapter, sessionPath(args.session));

  const { browser, close } = await launchBrowser();
  const ctx = await newAccountContext(browser, args.platform as ActionJob['platform'], state);
  const page = await ctx.newPage();
  try {
    // The URL is a comment permalink; opening it focuses the target comment.
    await openPost(page, args.url!);
    log(`[reply] opened ${page.url()}`);
    if (await needsLogin(page)) {
      log('[reply] AUTH WALL — session appears dead (redirected to login/checkpoint).');
      await maybeKeepOpen(args, ctx);
      return;
    }

    if (!args.commit) {
      const affordance = sel.replyButton ?? sel.commentButton;
      log(`[reply] DRY RUN — reply affordance: ${affordance ?? '(none pinned)'}`);
      const candidates = composerCandidates(sel);
      candidates.forEach((c, i) => log(`           composer [${i}] ${c}`));
      let match = await probeComposer(page, candidates);
      if (!match && affordance) {
        log(`[reply] no composer yet; clicking reply affordance...`);
        await page.locator(affordance).first().click({ timeout: 5_000 }).catch(() => log('[reply] (affordance click failed/absent)'));
        await page.waitForTimeout(1_000);
        match = await probeComposer(page, candidates);
      }
      if (!match) {
        log('[reply] RESULT: NO composer matched after opening reply. <-- fix sel replyButton / composerInputs');
      } else {
        log(`[reply] RESULT: matched composer [${match.index}]: ${match.selector} (DRY — not filled/submitted)`);
      }
    } else {
      log('[reply] COMMIT — running the real worker replyToComment() (this POSTS a REAL reply)...');
      const res = await replyToComment(
        { page, sel, job: makeJob(args.platform, args.url!, 'reply_comment', args.text) },
        args.text!,
      );
      log(`[reply] COMMIT result: ${JSON.stringify(res)}`);
    }
    await maybeKeepOpen(args, ctx);
  } finally {
    await close();
  }
}

/**
 * The composer probe. Reproduces dom.ts step 1+2 (probe candidates, else click
 * the comment affordance and re-probe) then FILLS the matched candidate — but
 * never submits. Prints exactly which candidate matched (or that none did):
 * this is the signal the operator uses to fix the selector list.
 */
async function commentDryRun(page: Page, sel: SelectorSet, text: string): Promise<void> {
  const candidates = composerCandidates(sel);
  log(`[comment] DRY RUN — candidate composer selectors (in order):`);
  candidates.forEach((c, i) => log(`           [${i}] ${c}`));
  if (candidates.length === 0) {
    log('[comment] no composer selectors pinned — nothing to probe.');
    return;
  }

  // 1. usually already mounted on a permalink.
  let match = await probeComposer(page, candidates);
  // 2. else click the comment affordance and re-probe.
  if (!match && sel.commentButton) {
    log(`[comment] no composer yet; clicking comment affordance: ${sel.commentButton}`);
    await page
      .locator(sel.commentButton)
      .first()
      .click({ timeout: 5_000 })
      .catch(() => log('[comment] (comment affordance click failed/absent)'));
    await page.waitForTimeout(1_000);
    match = await probeComposer(page, candidates);
  }

  if (!match) {
    log('[comment] RESULT: NO composer candidate matched. <-- fix sel/index.ts composerInputs');
    log(`[comment] (this reproduces the live failure: none of ${candidates.length} candidates present)`);
    return;
  }

  log(`[comment] RESULT: matched candidate [${match.index}]: ${match.selector}`);
  // Fill (reversible: types text, does NOT post), matching dom.ts step 3.
  await match.loc.click({ timeout: 3_000 }).catch(() => undefined);
  const filled = await match.loc
    .fill(text, { timeout: 5_000 })
    .then(() => true)
    .catch((e: Error) => {
      log(`[comment] fill FAILED on matched candidate: ${e.message}`);
      return false;
    });
  if (filled) {
    log(`[comment] DRY RUN — filled composer with "${text}" but did NOT submit.`);
  }
  log('[comment] dry run complete (no comment posted).');
}

async function maybeKeepOpen(args: Args, _ctx: BrowserContext): Promise<void> {
  if (!args.keepOpen) return;
  if (!stdin.isTTY) {
    log('[harness] --keep-open ignored (no interactive terminal).');
    return;
  }
  await pressEnter('[harness] Press ENTER to close the browser... ');
}

// --- entry ------------------------------------------------------------------

async function main(): Promise<void> {
  const args = parseArgs(process.argv.slice(2));
  log(`[harness] cmd=${args.cmd} platform=${args.platform} commit=${args.commit}`);
  if (!args.commit && args.cmd !== 'login') {
    log('[harness] MODE: DRY RUN (no like/comment submitted). Pass --commit to actually submit.');
  }
  switch (args.cmd) {
    case 'login':
      await cmdLogin(args);
      break;
    case 'like':
      await cmdLike(args);
      break;
    case 'comment':
      await cmdComment(args);
      break;
    case 'comment-reply':
      await cmdCommentReply(args);
      break;
    default:
      log(
        [
          '',
          'SMM local worker harness — headful Playwright against the REAL worker automation.',
          '',
          'Usage:',
          '  tsx src/run.ts login         --platform instagram',
          '  tsx src/run.ts like          --url <postUrl> [--platform instagram] [--commit] [--keep-open]',
          '  tsx src/run.ts comment       --url <postUrl> --text "OKE" [--platform instagram] [--commit] [--keep-open]',
          '  tsx src/run.ts comment-reply --url <commentPermalink> --text "OKE" [--platform instagram] [--commit]',
          '                               (comment permalink e.g. .../p/<post>/c/<commentId>/)',
          '',
          'Flags:',
          '  --platform  instagram (default) | threads',
          '  --url       target post permalink',
          '  --text      comment text',
          '  --session   storageState JSON path (default infra/backup/sessions/session-<platform>.json)',
          '  --commit    ACTUALLY submit (real, irreversible). Without it: dry run (fills, no submit).',
          '  --keep-open pause before closing the browser (interactive terminal only)',
          '',
        ].join('\n'),
      );
      if (args.cmd) fail(`unknown command "${args.cmd}"`);
  }
}

main().catch((err: unknown) => {
  fail((err as Error).stack ?? String(err));
});
