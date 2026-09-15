# 0002. Actions use Playwright; scraping uses Apify

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: worker, scraping, action

## Context and Problem Statement

We both read (scrape) and write (comment/like/report) on seven platforms. Scraping at scale needs rotating infrastructure and platform-specific actors; performing an authenticated action needs a real browser session tied to a specific account.

## Decision Drivers

- Scrape must scale out without us running huge infra.
- Actions must run from the account's real session/fingerprint.
- Keep one tool per concern (DRY, clear ownership).

## Considered Options

1. Use Apify for both scraping and actions.
2. Use Playwright for both.
3. Apify for scraping, Playwright for actions.

## Decision Outcome

Chosen option: **"Apify for scraping, Playwright for actions."** because each tool does what it is best at: Apify for cloud-scale read, Playwright for authenticated, fingerprinted, headful write.

### Consequences

- **Good**: Scrape scales cheaply; actions run headful on Xvfb with the account's own session.
- **Bad**: Two runtimes/toolchains to maintain and two sets of credentials paths.
- **Neutral**: Scrape credentials and action sessions are provisioned separately.

### Confirmation

Worker has no scrape code; scrapers never hold action sessions.

## Pros and Cons of the Options

### Apify for scraping, Playwright for actions. *(chosen)*

- **Good**: Scrape scales cheaply; actions run headful on Xvfb with the account's own session.
- **Bad**: Two runtimes/toolchains to maintain and two sets of credentials paths.

### Alternatives

- **Use Apify for both scraping and actions.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

- **Use Playwright for both.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- SYSTEM_DESIGN §Components; PLATFORM_MATRIX; P2-03 vs P3-*.
- See also: `DEVELOPMENT_RULE.md` §17.1, `PRD.md`, `ERD.md`, `SYSTEM_DESIGN.md`.
