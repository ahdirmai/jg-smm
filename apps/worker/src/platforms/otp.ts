/**
 * Shared OTP / 2FA primitives (P3-04+). The operator-driven login flow parks a
 * live context when a platform shows a verification wall; these helpers are the
 * one place that knows how to *detect* that wall and how to *answer* it.
 *
 * They are deliberately a leaf module — they import only Playwright types, never
 * `core/auth` — so both the adapters and `core/auth` can depend on them without
 * a cycle. An adapter builds its OTP surface from `otpHandlers(selector)`; a
 * platform that needs bespoke handling (e.g. a multi-box SMS code) overrides the
 * two methods instead.
 */
import type { BrowserContext } from 'playwright';

/**
 * The Meta 2FA / checkpoint code field, shared verbatim by Instagram + Threads
 * (identical markup). This is the historical hardcoded selector that used to
 * live in `core/auth`; it is now the default the Meta adapters opt into.
 */
export const META_OTP_SELECTOR =
  'input[name="verificationCode"], input[autocomplete="one-time-code"]';

/**
 * Default detection: a login needs operator input when any open page in the
 * context shows the platform's code field. Never throws — a detached/navigating
 * page simply counts as "no field", so the poll loop keeps its own timing.
 */
export async function detectAuthInputWith(
  ctx: BrowserContext,
  selector: string,
): Promise<boolean> {
  for (const page of ctx.pages()) {
    const count = await page
      .locator(selector)
      .count()
      .catch(() => 0);
    if (count > 0) return true;
  }
  return false;
}

/**
 * Default submit: fill the code into the last open page's single field and
 * press Enter. This is the single-input case (Meta, most web 2FA); a platform
 * whose challenge is a row of one-char boxes supplies its own `submitAuthInput`.
 */
export async function submitAuthInputWith(
  ctx: BrowserContext,
  value: string,
  selector: string,
): Promise<void> {
  const pages = ctx.pages();
  const page = pages[pages.length - 1];
  if (!page) return;
  const field = page.locator(selector).first();
  await field.fill(value);
  await field.press('Enter');
}

/** The OTP slice of a `PlatformAdapter`. */
export interface OtpHandlers {
  otpFieldSelector: string;
  detectAuthInput: (ctx: BrowserContext) => Promise<boolean>;
  submitAuthInput: (ctx: BrowserContext, value: string) => Promise<void>;
}

/**
 * Build the default detect/submit pair bound to one CSS selector (DRY). An
 * adapter spreads the result into its object; IG and Threads both pass
 * `META_OTP_SELECTOR`, so the Meta behaviour lives in exactly one place.
 */
export function otpHandlers(selector: string): OtpHandlers {
  return {
    otpFieldSelector: selector,
    detectAuthInput: (ctx) => detectAuthInputWith(ctx, selector),
    submitAuthInput: (ctx, value) => submitAuthInputWith(ctx, value, selector),
  };
}
