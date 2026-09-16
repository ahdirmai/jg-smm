'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { AlertCircle, ArrowLeft, CheckCircle2, Loader2, Upload } from 'lucide-react';

import { Button, Card, CardContent, Textarea } from '@smm/ui';
import { api, type CreateAccountRequest, type ImportResult } from '@/lib/api';

const MAX_ROWS = 100;
const FIELDS = ['platform', 'username', 'password'] as const;

/**
 * Bulk import (P6-08, mirrors docs/prototype/bulk-import.html).
 *
 * The operator pastes CSV — one account per line, `platform,username,password`.
 * Parsing stays deliberately strict and dumb: three comma fields, no quoting
 * grammar, because anything an operator pastes from a sheet that has a quoted
 * comma inside a password is a data problem worth surfacing as an invalid row
 * rather than silently mis-splitting. Same rule as ImportAccountsDialog.
 */
export default function ImportAccountsPage() {
  const router = useRouter();
  const [csv, setCsv] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);

  const parse = (text: string): CreateAccountRequest[] =>
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
          tags: [],
        };
      });

  const submit = async () => {
    setError(null);
    setResult(null);
    const rows = parse(csv);
    if (rows.length === 0) {
      setError('Nothing to import — paste at least one `platform,username,password` line.');
      return;
    }
    if (rows.length > MAX_ROWS) {
      setError(`Too many rows: ${rows.length}. The import cap is ${MAX_ROWS} per batch.`);
      return;
    }
    setBusy(true);
    try {
      const res = await api.importAccounts(rows);
      setResult(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Import failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <Button variant="ghost" size="sm" onClick={() => router.back()} className="-ml-2">
        <ArrowLeft />
        Back to accounts
      </Button>

      <Card>
        <CardContent className="space-y-4 p-6">
          <div>
            <h2 className="text-sm font-semibold">Bulk import accounts</h2>
            <p className="text-xs text-muted-foreground">
              One account per line: <code className="font-mono text-xs">platform,username,password</code>.
              Up to {MAX_ROWS} rows per batch. Passwords are sealed at rest.
            </p>
          </div>

          <Textarea
            value={csv}
            onChange={(e) => setCsv(e.target.value)}
            placeholder={'instagram, growth.hacks.id, ••••••••\nthreads, growth.hacks.id, ••••••••'}
            className="min-h-40 font-mono text-xs"
            spellCheck={false}
          />

          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void submit()} disabled={busy || !csv.trim()}>
              {busy ? <Loader2 className="animate-spin" /> : <Upload />}
              Import
            </Button>
            <Button variant="outline" onClick={() => setCsv('')} disabled={busy || !csv.trim()}>
              Clear
            </Button>
          </div>

          {error ? (
            <p className="flex items-center gap-2 text-sm text-destructive" role="alert">
              <AlertCircle className="size-4 shrink-0" />
              {error}
            </p>
          ) : null}

          {result ? (
            <div className="space-y-3 rounded-lg border p-4">
              <div className="flex items-center gap-2 text-sm font-medium">
                <CheckCircle2 className="size-4 text-success" />
                {result.queued} account{result.queued === 1 ? '' : 's'} imported
              </div>
              {result.invalid.length > 0 ? (
                <div className="space-y-1.5">
                  <p className="text-xs font-medium text-muted-foreground">
                    {result.invalid.length} row{result.invalid.length === 1 ? '' : 's'} rejected:
                  </p>
                  <ul className="space-y-1 text-xs">
                    {result.invalid.map((row, i) => (
                      <li key={i} className="flex gap-2 text-destructive">
                        <span className="font-mono shrink-0">line {row.row + 1}</span>
                        <span>{row.reason}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}
              <div className="flex gap-2 pt-1">
                <Button size="sm" variant="outline" onClick={() => router.push('/accounts')}>
                  View accounts
                </Button>
              </div>
            </div>
          ) : null}
        </CardContent>
      </Card>

      <p className="text-xs text-muted-foreground">
        Fields: {FIELDS.join(', ')}. A duplicate (same platform + username on a worker) is rejected,
        never silently overwritten.
      </p>
    </div>
  );
}
