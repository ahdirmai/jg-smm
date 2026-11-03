'use client';

/**
 * Copy-to-clipboard button. Falls back to a hidden textarea where the async
 * Clipboard API is unavailable (a non-secure origin), so it never silently
 * no-ops — a copy button that does nothing is worse than none, because the
 * operator pastes whatever was there before.
 *
 * Shared by the batch detail page and the post detail dialog; it lived inline
 * in the first one, and a second copy would drift in exactly the way the
 * permalink helpers did.
 */

import { Button } from '@smm/ui';
import { Check, Copy } from 'lucide-react';
import { useState } from 'react';

export function CopyButton({
  value,
  label,
  text = 'Copy',
}: {
  value: string;
  label: string;
  /** Button text; the post dialog uses a longer one than the page's default. */
  text?: string;
}) {
  const [done, setDone] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      const el = document.createElement('textarea');
      el.value = value;
      el.style.position = 'fixed';
      el.style.opacity = '0';
      document.body.appendChild(el);
      el.select();
      try {
        document.execCommand('copy');
      } catch {
        return;
      } finally {
        document.body.removeChild(el);
      }
    }
    setDone(true);
    setTimeout(() => setDone(false), 1500);
  }

  return (
    <Button variant="outline" size="sm" onClick={copy} aria-label={label}>
      {done ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      {done ? 'Copied' : text}
    </Button>
  );
}
