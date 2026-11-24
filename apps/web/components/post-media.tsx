'use client';

/**
 * Post media carousel: one slide at a time, swipeable, with arrows and dots,
 * plus a click-to-open lightbox for the full-size view.
 *
 * Native scroll-snap rather than a JS carousel: the browser already does
 * momentum, rubber-banding, touch/trackpad swipe and snap for free, and a JS
 * one would reimplement all of it worse. The track IS the state — the arrows
 * and dots only call scrollTo, and the active dot is derived from scrollLeft,
 * so there is no index to drift out of sync with what is on screen.
 *
 * Plain <img>: media is remote user content on rotating CDN buckets
 * (scontent-<region>.cdninstagram.com), so next/image remotePatterns is the
 * wrong tool for a dashboard preview.
 */

import { Button, Dialog, DialogContent } from '@smm/ui';
import { ChevronLeft, ChevronRight, X } from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';

export function PostMedia({ urls, compact = false }: { urls: string[] | null; compact?: boolean }) {
  const items = urls ?? [];
  const track = useRef<HTMLDivElement>(null);
  const [index, setIndex] = useState(0);
  const [open, setOpen] = useState<number | null>(null);

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

  /** Scroll to a slide. Reduced motion gets an instant jump, not a glide. */
  const goTo = useCallback((i: number) => {
    const el = track.current;
    if (!el) return;
    const still = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    el.scrollTo({ left: i * el.clientWidth, behavior: still ? 'auto' : 'smooth' });
  }, []);

  const onScroll = useCallback(() => {
    const el = track.current;
    if (!el || el.clientWidth === 0) return;
    setIndex(Math.round(el.scrollLeft / el.clientWidth));
  }, []);

  if (items.length === 0) return null;

  // Clamp in case the parent swaps posts while the lightbox is open.
  const cur = open !== null && open < items.length ? open : null;
  const many = items.length > 1;

  return (
    <>
      {/* One grid item: the carousel and its dots must not become two children
          of a two-column preview grid, or the caption wraps below the media. */}
      <div className="space-y-2">
        {/* Capped width: an uncapped square slide fills the whole column on a
          desktop and buries the rest of the form below the fold. */}
        <div
          className={`relative mx-auto overflow-hidden rounded-xl border border-border/60 bg-secondary/40 ${
            compact ? 'max-w-[240px]' : 'max-w-[380px]'
          }`}
        >
          <div
            ref={track}
            onScroll={onScroll}
            className="flex snap-x snap-mandatory overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
            tabIndex={many ? 0 : -1}
            role="group"
            aria-roledescription="carousel"
            aria-label={`Post media, ${items.length} item${many ? 's' : ''}`}
          >
            {items.map((u, i) => (
              <button
                key={i}
                type="button"
                onClick={() => setOpen(i)}
                className="relative block aspect-square w-full shrink-0 snap-center focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring"
                aria-label={`Media ${i + 1} of ${items.length}, open full size`}
              >
                {/* eslint-disable-next-line @next/next/no-img-element -- remote user content, rotating CDN hosts */}
                <img
                  src={u}
                  alt=""
                  loading={i === 0 ? 'eager' : 'lazy'}
                  draggable={false}
                  className="h-full w-full object-cover"
                />
              </button>
            ))}
          </div>

          {many ? (
            <>
              {/* Arrows, inside soft squares per §3.6 — black as the second
                accent on the glass ground, never a second saturated hue. */}
              <button
                type="button"
                onClick={() => goTo(Math.max(0, index - 1))}
                disabled={index === 0}
                aria-label="Previous media"
                className="absolute left-2 top-1/2 -translate-y-1/2 rounded-lg bg-background/80 p-1.5 text-foreground backdrop-blur-none transition-opacity hover:bg-background disabled:pointer-events-none disabled:opacity-0"
              >
                <ChevronLeft className="size-4" />
              </button>
              <button
                type="button"
                onClick={() => goTo(Math.min(items.length - 1, index + 1))}
                disabled={index === items.length - 1}
                aria-label="Next media"
                className="absolute right-2 top-1/2 -translate-y-1/2 rounded-lg bg-background/80 p-1.5 text-foreground transition-opacity hover:bg-background disabled:pointer-events-none disabled:opacity-0"
              >
                <ChevronRight className="size-4" />
              </button>

              {/* Counter once the dots stop being legible as individual targets. */}
              {items.length > 8 ? (
                <span className="absolute right-2 top-2 rounded-md bg-foreground/70 px-1.5 py-0.5 text-[11px] font-medium text-background">
                  {index + 1}/{items.length}
                </span>
              ) : null}
            </>
          ) : null}
        </div>

        {many ? (
          <div className="flex items-center justify-center gap-1.5">
            {items.map((_, i) => (
              <button
                key={i}
                type="button"
                onClick={() => goTo(i)}
                aria-label={`Go to media ${i + 1}`}
                aria-current={i === index}
                className={`size-1.5 rounded-full transition-colors ${
                  i === index ? 'bg-foreground' : 'bg-border hover:bg-muted-foreground'
                }`}
              />
            ))}
          </div>
        ) : null}
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
