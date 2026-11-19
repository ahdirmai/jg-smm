'use client';

import { useEffect, useState } from 'react';

import { cn } from '@smm/ui';

/**
 * Turn a heartbeat URL into a noVNC client page the iframe can load directly.
 *
 * The heartbeat reports host:port (or a bare root), and noVNC serves a
 * directory listing there. `vnc.html` is the client; `autoconnect=1` skips the
 * Connect dialog so the screen streams immediately, and `resize=scale` fits the
 * remote framebuffer into the frame instead of clipping it at native size. A
 * URL already pointing at the client is left alone — only its query params are merged.
 */
export function novncClientUrl(url: string): string {
  // A TICKETED URL is used verbatim. Its `path=/websockify?ticket=...`
  // parameter is the authorisation the worker checks, and re-serialising the
  // query (or appending a second `?`) breaks the signature. The API already
  // returns `autoconnect=true&resize=scale`, so nothing needs adding.
  if (new URLSearchParams(url.split('?')[1] ?? '').has('path')) {
    return url.trim();
  }
  const cleaned = url.trim().replace(/\/+$/, '');
  const qIndex = cleaned.indexOf('?');
  const path = qIndex === -1 ? cleaned : cleaned.slice(0, qIndex);
  const query = qIndex === -1 ? '' : cleaned.slice(qIndex + 1);
  const params = new URLSearchParams(query);
  params.set('autoconnect', '1');
  // noVNC defaults to `resize=off`: the 1280x800 Xvfb framebuffer renders at
  // native size and gets clipped at the iframe edge. `resize=scale` fits the
  // whole remote into the container (aspect-preserved, no letterboxing — the
  // display and this frame share the 16:10 ratio).
  params.set('resize', 'scale');
  if (!path.endsWith('vnc.html')) {
    return `${path}/vnc.html?${params.toString()}`;
  }
  return `${path}?${params.toString()}`;
}

/**
 * LiveView — the noVNC iframe body, shared by the live browser modal and the
 * account detail drawer.
 *
 * It mounts the iframe on mount, so the CALLER owns the websocket: the modal
 * renders it only while open, the drawer only while its panel is open AND the
 * live view is selected. A hidden noVNC client still holds a websocket open and
 * would pin a worker display forever — that is why the lazy-load decision
 * lives in the parents, not here.
 */
export function LiveView({
  url,
  title,
  className,
}: {
  url: string;
  title: string;
  className?: string;
}) {
  // Mounted straight away for a ticketed URL: the ticket expires, and a second
  // of delay on first render is a second the operator may spend staring at a
  // blank frame. The legacy path still normalises after mount.
  const [src, setSrc] = useState<string | null>(() =>
    url.includes('ticket=') ? url.trim() : null,
  );
  useEffect(() => {
    setSrc(novncClientUrl(url));
  }, [url]);

  return (
    <div className={cn('relative aspect-[16/10] w-full overflow-hidden rounded-lg bg-black/95', className)}>
      {src ? (
        <iframe
          src={src}
          title={title}
          className="absolute inset-0 size-full border-0"
          allow="clipboard-read; clipboard-write"
          // The ticket lives in this URL. Without this it rides along as the
          // Referer of every asset request the noVNC client makes, handing the
          // credential to anything that logs referrers.
          referrerPolicy="no-referrer"
        />
      ) : null}
    </div>
  );
}
