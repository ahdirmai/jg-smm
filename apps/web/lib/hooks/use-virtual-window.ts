'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

/**
 * Fixed-height row virtualization (P4-02).
 *
 * The queue is unbounded in principle and the ticket's AC is ">1000 baris
 * lancar". Rendering a thousand table rows in React is fine; re-rendering them
 * on every poll tick is not. So we window: measure the visible slice from the
 * scroll position and translate a spacer.
 *
 * No dependency: @tanstack/react-virtual is the same algorithm with more
 * knobs (dynamic heights, lanes). Rows here are uniform by construction, so
 * fixed-height windowing is the edge-case-correct choice.
 */
export function useVirtualRowWindow(rowHeight: number, overscan = 10) {
  const ref = useRef<HTMLDivElement>(null);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewport, setViewport] = useState(0);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    // Track the visible height and scroll offset. rAF-free: the scroll handler
    // only stores numbers; React re-renders the windowed slice.
    const onScroll = () => setScrollTop(el.scrollTop);
    const onResize = () => setViewport(el.clientHeight);
    onResize();
    el.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('resize', onResize);
    return () => {
      el.removeEventListener('scroll', onScroll);
      window.removeEventListener('resize', onResize);
    };
  }, []);

  const slice = useCallback(
    (total: number) => {
      const start = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan);
      const visible = Math.ceil(viewport / rowHeight);
      const end = Math.min(total, start + visible + overscan * 2);
      return {
        start,
        end,
        // Spacer before/after keep the scrollbar proportional.
        before: start * rowHeight,
        after: Math.max(0, (total - end) * rowHeight),
      };
    },
    [rowHeight, overscan, scrollTop, viewport],
  );

  return { ref, slice };
}
