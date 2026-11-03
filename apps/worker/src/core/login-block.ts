/**
 * Login-wall detection: WHY a login is not progressing.
 *
 * The poll loop needs three distinct verdicts where it previously had one:
 *
 *   verified     cookies are present (proven)
 *   needs_input  a code field is showing — the operator answers it
 *   blocked      a wall that no amount of waiting will clear
 *
 * That third case used to fall through to the timeout, and every wall produced
 * the same unactionable sentence ("submit did not complete"), so a fleet run
 * wrote ~400 identical rows into the operator's sheet and hid the one thing
 * they needed to know: a wrong password is a CREDENTIAL problem to fix, a
 * CAPTCHA is an EGRESS problem, and a checkpoint needs a human. Verified live:
 * Instagram showed "The login information you entered is incorrect" while the
 * worker reported "still on https://www.instagram.com/accounts/login/".
 *
 * Detection reads the rendered page, not the URL alone: the wrong-password
 * banner keeps the login URL, so URL matching alone cannot see it.
 *
 * Leaf module on purpose — imports only Playwright types — so `core/auth` and
 * the adapters can both depend on it without a cycle.
 */
import type { BrowserContext, Page } from 'playwright';

/** Why a login is stuck, in the operator's terms. */
export type LoginBlock =
  | 'wrong_password'
  | 'captcha'
  | 'checkpoint'
  | 'rate_limited'
  | 'account_locked';

/** The banner Meta renders when the username/password pair is rejected. */
const WRONG_PASSWORD_TEXT = [
  'the login information you entered is incorrect',
  'password you entered is incorrect',
  'incorrect password',
  "couldn't find your account",
  'the username you entered',
];

/** A checkpoint the operator resolves, but only inside the platform's UI. */
const CHECKPOINT_TEXT = [
  'confirm it',
  "we've detected suspicious",
  'suspicious login attempt',
  'enter the security code',
  'send security code',
  'help us confirm',
];

const RATE_LIMIT_TEXT = [
  'try again later',
  'too many requests',
  'please wait a few minutes',
  'you’re temporarily blocked',
  "you're temporarily blocked",
];

const LOCKED_TEXT = [
  'your account has been disabled',
  'account has been locked',
  'we suspended your account',
];

// The wall shows up in three shapes, all of which must match:
//   1. the widget page itself            /auth_platform/recaptcha
//   2. a challenge/checkpoint path       /challenge/, /checkpoint/
//   3. a BOUNCED login, where the platform redirects to auth_platform for a
//      CAPTCHA and then back to the form because the token was never solved.
//      The URL then reads /accounts/login/?next=<encoded auth_platform ...cap=>
//      — the only trace is inside the encoded `next` parameter, which is why
//      this was invisible before: the page looked like a plain, empty form.
const CAPTCHA_URL =
  /\/auth_platform\/(?:recaptcha|\?)|\/challenge\/|\/checkpoint\/|recaptcha\/enterprise|[?&]next=[^&]*auth_platform|[?&]cap=/i;

/**
 * Classify one page. Returns undefined when nothing conclusive is up, which is
 * the normal state while the form is filling and submitting.
 *
 * Order matters: a CAPTCHA frame sits on the login page and a checkpoint can
 * follow a successful password, so the credential verdict is checked last —
 * a wrong-password banner is definitive and should win over a URL that merely
 * looks like a challenge path.
 */
async function classifyPage(page: Page): Promise<LoginBlock | undefined> {
  let url = '';
  try {
    url = page.url();
  } catch {
    return undefined; // mid-navigation: not a verdict
  }

  // reCAPTCHA is cross-origin, so its text is unreadable; the URL is the proof.
  if (CAPTCHA_URL.test(url)) return 'captcha';

  const body = await page
    .locator('body')
    .innerText({ timeout: 2_000 })
    .catch(() => '');
  if (!body) return undefined;
  const text = body.toLowerCase();

  // Credentials first: a rejected password outranks every other banner.
  if (WRONG_PASSWORD_TEXT.some((t) => text.includes(t))) return 'wrong_password';
  if (LOCKED_TEXT.some((t) => text.includes(t))) return 'account_locked';
  if (RATE_LIMIT_TEXT.some((t) => text.includes(t))) return 'rate_limited';
  if (CHECKPOINT_TEXT.some((t) => text.includes(t))) return 'checkpoint';
  return undefined;
}

/**
 * Classify the whole context, newest page last. Never throws: a detached page
 * counts as "nothing conclusive" so the caller keeps polling on its own clock.
 */
export async function detectLoginBlock(ctx: BrowserContext): Promise<LoginBlock | undefined> {
  const pages = ctx.pages();
  for (let i = pages.length - 1; i >= 0; i -= 1) {
    const block = await classifyPage(pages[i]!);
    if (block) return block;
  }
  return undefined;
}

/**
 * The reason string stored on the account row. Stable prefixes so the wave
 * script can map a verdict without parsing prose: "wrong_password: ...".
 */
export function blockReason(block: LoginBlock, detail: string): string {
  return `${block}: ${detail}`;
}
