'use client';

/**
 * Scrape page. Keyword search lives here — the on-demand single-post scrape
 * stays inside the New Action form on /actions because its result feeds the
 * enqueue flow directly.
 */

import { KeywordScrapeForm } from './keyword-scrape-form';

export default function ScrapePage() {
  return (
    <div className="mx-auto max-w-6xl space-y-4">
      <KeywordScrapeForm />
    </div>
  );
}
