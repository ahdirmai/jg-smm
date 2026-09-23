'use client';

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@smm/ui';
import { ExternalLink, MonitorPlay } from 'lucide-react';
import { useEffect, useState } from 'react';

/**
 * Turn a heartbeat URL into a noVNC client page the iframe can load directly.
 *
 * The heartbeat reports host:port (or a bare root), and noVNC serves a
 * directory listing there. `vnc.html` is the client; `autoconnect=1` skips the
 * Connect dialog so the screen streams immediately. A URL already pointing at
 * the client is left alone — only its query params are merged.
 */
function novncClientUrl(url: string): string {
  const cleaned = url.trim().replace(/\/+$/, '');
  const qIndex = cleaned.indexOf('?');
  const path = qIndex === -1 ? cleaned : cleaned.slice(0, qIndex);
  const query = qIndex === -1 ? '' : cleaned.slice(qIndex + 1);
  const params = new URLSearchParams(query);
  params.set('autoconnect', '1');
  if (!path.endsWith('vnc.html')) {
    return `${path}/vnc.html?${params.toString()}`;
  }
  return `${path}?${params.toString()}`;
}

/**
 * LiveBrowserModal (P4-08). Embeds a worker's noVNC view in an iframe so an
 * operator can watch (or drive) the headful browser during a login.
 *
 * The URL comes from the worker's heartbeat (`novncUrl`), never guessed: an
 * unpublished worker has no live view, and the button that opens this modal is
 * hidden rather than linking to a dead URL.
 *
 * The heartbeat publishes the *host:port*, not a path. noVNC's root is a
 * directory listing, so the client page is appended here (`vnc.html`) — one
 * normalization point, and the operator lands on the screen instead of a file
 * index they then have to click into. `autoconnect=1` takes it the rest of the
 * way: no "Connect" button, the framebuffer streams as soon as the socket is
 * up.
 *
 * The iframe is only mounted while the modal is open — a hidden noVNC client
 * still holds a websocket open and would pin a worker display forever.
 */
export function LiveBrowserModal({
  open,
  onOpenChange,
  url,
  workerName,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  url: string;
  workerName: string;
}) {
  // Loaded only after the dialog is actually open, so a closed modal never
  // opens a websocket to the worker.
  const [src, setSrc] = useState<string | null>(null);
  useEffect(() => {
    if (open) setSrc(url ? novncClientUrl(url) : null);
    else setSrc(null);
  }, [open, url]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] gap-0 overflow-hidden p-0 sm:max-w-[900px]">
        <DialogHeader className="border-b px-4 py-3">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2">
              <MonitorPlay className="size-4 text-muted-foreground" />
              <div className="space-y-0.5">
                <DialogTitle className="text-sm">Live browser — {workerName}</DialogTitle>
                <DialogDescription className="font-mono text-xs">{url}</DialogDescription>
              </div>
            </div>
            <a
              href={src ?? url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            >
              <ExternalLink className="size-3.5" />
              Open in tab
            </a>
          </div>
        </DialogHeader>
        <div className="relative aspect-[16/10] w-full bg-black/95">
          {src ? (
            <iframe
              src={src}
              title={`Live browser — ${workerName}`}
              className="absolute inset-0 size-full border-0"
              allow="clipboard-read; clipboard-write"
            />
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  );
}
