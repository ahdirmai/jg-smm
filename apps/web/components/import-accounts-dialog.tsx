'use client';

import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Textarea,
} from '@smm/ui';
import { AlertCircle, Upload } from 'lucide-react';
import { useState } from 'react';

import { api, type CreateAccountRequest, type ImportResult } from '@/lib/api';

const MAX_ROWS = 100;

/**
 * Bulk account import (P4-07). The operator pastes CSV (`platform,username,password`)
 * — one account per line. Parsing is deliberately strict and dumb: three comma
 * fields, no quoting grammar, because anything an operator pastes from a sheet
 * that has a quoted comma inside a password is a data problem worth surfacing
 * as an invalid row rather than silently mis-splitting.
 */
export function ImportAccountsDialog({
  open,
  onOpenChange,
  onImported,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onImported?: () => void;
}) {
  const [csv, setCsv] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);

  const reset = () => {
    setCsv('');
    setError(null);
    setResult(null);
  };

  const close = () => {
    onOpenChange(false);
    setTimeout(reset, 200);
  };

  const parse = (text: string) =>
    text
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line.length > 0 && !line.toLowerCase().startsWith('platform,'))
      .map((line) => {
        const parts = line.split(',');
        return {
          platform: (parts[0] ?? '').trim().toLowerCase() as CreateAccountRequest['platform'],
          username: (parts[1] ?? '').trim(),
          password: (parts[2] ?? '').trim(),
          tags: [] as string[],
        };
      });

  const submit = async () => {
    setError(null);
    setResult(null);

    const rows = parse(csv);
    if (rows.length === 0) {
      setError('Paste at least one row: platform,username,password');
      return;
    }
    if (rows.length > MAX_ROWS) {
      setError(`An import is capped at ${MAX_ROWS} rows; you pasted ${rows.length}.`);
      return;
    }
    const bad = rows.find((r) => !r.username || !r.password || !r.platform);
    if (bad) {
      setError('Every row needs platform, username and password (3 comma fields).');
      return;
    }

    setBusy(true);
    try {
      const res = await api.importAccounts(rows);
      setResult(res);
      onImported?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Import failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && close()}>
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>Import accounts</DialogTitle>
          <DialogDescription>
            {
              'One account per line as platform,username,password. Up to 100 rows; 10 imports per minute.'
            }
          </DialogDescription>
        </DialogHeader>

        {result ? (
          <div className="space-y-3 py-2">
            <div className="flex items-center gap-3 text-sm">
              <span className="font-medium text-green-600 dark:text-green-500">
                {result.queued} queued
              </span>
              {result.invalid.length > 0 ? (
                <span className="font-medium text-destructive">
                  {result.invalid.length} invalid
                </span>
              ) : null}
              {result.rateLimited ? (
                <span className="font-medium text-destructive">rate limited</span>
              ) : null}
            </div>
            {result.invalid.length > 0 ? (
              <div className="max-h-48 overflow-y-auto rounded-lg border border-border/60">
                {result.invalid.map((e, i) => (
                  <div
                    key={i}
                    className="flex items-start gap-2 border-b px-3 py-2 text-xs last:border-b-0"
                  >
                    <span className="font-mono text-muted-foreground">row {e.row}</span>
                    <span className="text-destructive">{e.reason}</span>
                  </div>
                ))}
              </div>
            ) : null}
          </div>
        ) : (
          <div className="space-y-4">
            <Textarea
              value={csv}
              onChange={(e) => setCsv(e.target.value)}
              placeholder={'instagram,growth.id,s3cret\nthreads,@growth.id,s3cret'}
              rows={7}
              className="font-mono text-xs"
              disabled={busy}
            />
            {error ? (
              <div className="flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                <AlertCircle className="size-4" />
                {error}
              </div>
            ) : null}
          </div>
        )}

        <DialogFooter>
          {result ? (
            <Button onClick={close}>Done</Button>
          ) : (
            <>
              <Button variant="outline" onClick={close} disabled={busy}>
                Cancel
              </Button>
              <Button onClick={() => void submit()} disabled={busy}>
                <Upload />
                Import
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
