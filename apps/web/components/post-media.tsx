'use client';

/**
 * Post media preview: a thumbnail grid — every carousel item, not just the
 * cover — with a click-to-open lightbox (prev/next, arrow keys, Esc to close).
 * Plain <img>: media is remote user content on rotating CDN buckets
 * (scontent-<region>.cdninstagram.com), so next/image remotePatterns is the
 * wrong tool for a dashboard preview.
 */

import { Button, Dialog, DialogContent } from '@smm/ui';
import { ChevronLeft, ChevronRight, X } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';

export function PostMedia({ urls }: { urls: string[] | null }) {
  const [open, setOpen] = useState<number | null>(null);
  const items = urls ?? [];

  const close = useCallback(() => setOpen(null), []);
  const step = useCallback(
    (d: number) => setOpen((i) => (i === null ? i : (i + d + items.length) % items.length)),
    [items.length],
  );

  // Arrow keys / Escape drive the lightbox while it is open.
  useEffect(() => {
    if (open === null) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close();
      else if (e.key === 'ArrowLeft') step(-1);
      else if (e.key === 'ArrowRight') step(1);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, close, step]);

  if (items.length === 0) return null;

  // Clamp in case the parent swaps posts while the lightbox is open.
  const cur = open !== null && open < items.length ? open : null;
  const cols = items.length === 1 ? 'grid-cols-1' : items.length >= 5 ? 'grid-cols-3' : 'grid-cols-2';

  return (
    <>
      <div className={`grid gap-1.5 ${cols} ${items.length === 1 ? 'max-w-[260px]' : 'sm:max-w-md'}`}>
        {items.map((u, i) => (
          <button
            key={i}
            type="button"
            onClick={() => setOpen(i)}
            className="block aspect-square w-full overflow-hidden rounded-md border bg-muted/40 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            aria-label={`Open media ${i + 1} of ${items.length}`}
          >
            {/* eslint-disable-next-line @next/next/no-img-element -- remote user content, rotating CDN hosts */}
            <img src={u} alt="" loading="lazy" className="h-full w-full object-cover" />
          </button>
        ))}
      </div>

      <Dialog open={cur !== null} onOpenChange={(o) => !o && close()}>
        <DialogContent className="max-w-3xl p-2">
          {cur !== null ? (
            <div className="relative flex flex-col items-center gap-2">
              {/* eslint-disable-next-line @next/next/no-img-element -- remote user content */}
              <img
                src={items[cur]}
                alt={`Media ${cur + 1} of ${items.length}`}
                className="max-h-[70vh] w-auto rounded-md object-contain"
              />
              <div className="flex w-full items-center justify-between">
                <Button type="button" variant="outline" size="sm" onClick={() => step(-1)}>
                  <ChevronLeft /> Prev
                </Button>
                <span className="text-xs text-muted-foreground">
                  {cur + 1} / {items.length}
                </span>
                <Button type="button" variant="outline" size="sm" onClick={() => step(1)}>
                  Next <ChevronRight />
                </Button>
              </div>
              <button
                type="button"
                onClick={close}
                className="absolute right-3 top-3 rounded-md p-1 hover:bg-accent"
                aria-label="Close"
              >
                <X className="size-4" />
              </button>
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  );
}
