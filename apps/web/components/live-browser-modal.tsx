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
 * LiveBrowserModal (P4-08). Embeds a worker's noVNC view in an iframe so an
 * operator can watch (or drive) the headful browser during a login.
 *
 * The URL comes from the worker's heartbeat (`novncUrl`), never guessed: an
 * unpublished worker has no live view, and the button that opens this modal is
 * hidden rather than linking to a dead URL.
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
    if (open) setSrc(url);
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
