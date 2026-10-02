'use client';

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@smm/ui';
import { ExternalLink, MonitorPlay } from 'lucide-react';

import { LiveView, novncClientUrl } from './live-view';

/**
 * LiveBrowserModal (P4-08). Embeds a worker's noVNC view in an iframe so an
 * operator can watch (or drive) the headful browser during a login.
 *
 * The URL comes from the worker's heartbeat (`novncUrl`), never guessed: an
 * unpublished worker has no live view, and the button that opens this modal is
 * hidden rather than linking to a dead URL.
 *
 * The heartbeat publishes the *host:port*, not a path. noVNC's root is a
 * directory listing, so the client page is appended in `novncClientUrl` and
 * `autoconnect=1` takes the rest of the way: no "Connect" button, the
 * framebuffer streams as soon as the socket is up.
 *
 * `LiveView` is only mounted while the modal is open — a hidden noVNC client
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
              href={url ? novncClientUrl(url) : undefined}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            >
              <ExternalLink className="size-3.5" />
              Open in tab
            </a>
          </div>
        </DialogHeader>
        {open ? <LiveView url={url} title={`Live browser — ${workerName}`} /> : null}
      </DialogContent>
    </Dialog>
  );
}
