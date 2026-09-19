import { test, expect } from '../fixtures/base.js';

/**
 * Dead-link sweep. The nav list in monitoring.spec.ts proves each sidebar
 * entry routes. This proves every internal <a href> the shell renders reaches
 * a real 200 and not an error boundary — including the monitoring subroutes
 * the sidebar generates per platform, which the nav list does not enumerate.
 *
 * Every dashboard page holds the SSE stream open, so the response the browser
 * gets is the page itself; a link to a route that 404s or throws renders the
 * Next error view, and that is what this catches.
 */

const PLATFORM_SUBROUTES = [
  'Instagram',
  'Threads',
  'Facebook',
  'LinkedIn',
  'X',
  'YouTube',
  'TikTok',
];

/** The pages that render the dashboard shell, walked for links. */
const PAGES = [
  '/',
  '/workers',
  '/accounts',
  '/actions',
  '/templates',
  '/monitoring',
  '/reports',
  '/audit',
  '/settings',
];

test.describe('dead links', () => {
  for (const path of PAGES) {
    test(`every internal link on ${path} resolves`, async ({ authedPage }) => {
      await authedPage.goto(path);

      // A page that failed to mount renders no shell links, so the header is
      // the cheap signal that the page itself loaded.
      await expect(authedPage.getByRole('banner')).toBeVisible();

      const hrefs = await authedPage.evaluate(() => {
        const out = new Set<string>();
        for (const a of document.querySelectorAll('a[href]')) {
          const href = a.getAttribute('href');
          if (!href) continue;
          // External, mailto, and hash targets are out of scope.
          if (/^([a-z]+:)?\/\//i.test(href)) continue;
          if (href.startsWith('#')) continue;
          out.add(href);
        }
        return Array.from(out);
      });

      expect(hrefs.length, `${path} rendered no internal links`).toBeGreaterThan(0);

      for (const href of hrefs) {
        // Check the route through the browser's own fetcher: this is the same
        // origin the link would navigate to, and it reports a 404 or a 500
        // without spending a navigation per link.
        const status = await authedPage.evaluate(async (url) => {
          const res = await fetch(url, { redirect: 'follow' });
          return res.status;
        }, href);

        expect(status, `${path} -> ${href}`).toBe(200);
      }
    });
  }

  for (const label of PLATFORM_SUBROUTES) {
    test(`the monitoring ${label} subroute renders`, async ({ authedPage }) => {
      // The sidebar generates these from the platform list; a route that only
      // exists for one platform is a silent dead link for the rest.
      await authedPage.goto('/monitoring');
      await authedPage.getByRole('link', { name: label, exact: true }).click();

      await expect(authedPage).toHaveURL(new RegExp(`\\/monitoring\\/[a-z]+$`));
      await expect(authedPage.getByRole('banner')).toBeVisible();
    });
  }
});
