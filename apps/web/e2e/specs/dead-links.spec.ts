import { test, expect } from '../fixtures/base.js';

/**
 * Dead-link sweep. Every internal <a href> the shell renders must reach a real
 * 200 and not an error boundary; the removed Monitoring and Templates routes
 * are intentionally absent from this list.
 *
 * Every dashboard page holds the SSE stream open, so the response the browser
 * gets is the page itself; a link to a route that 404s or throws renders the
 * Next error view, and that is what this catches.
 */

/** The pages that render the dashboard shell, walked for links. */
const PAGES = [
  '/',
  '/workers',
  '/accounts',
  '/actions',
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

});
