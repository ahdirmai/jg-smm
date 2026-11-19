'use client';

/**
 * Account picker for the action form.
 *
 * The fleet is ~500 rows and only a fraction can act at any moment, so a flat
 * scroll of every account was both endless and wrong: it offered accounts that
 * would fail at enqueue. This picker shows ONLY authenticated accounts, makes
 * search the primary surface, and keeps the selection visible in a sticky
 * summary that survives scrolling.
 */

import { Badge, Button, Input, Textarea } from '@smm/ui';
import { Search, X } from 'lucide-react';
import { useMemo } from 'react';

import type { Account } from '@/lib/api';
import { useVirtualRowWindow } from '@/lib/hooks/use-virtual-window';

// One row height for every state (collapsed and with a composer): the window
// math is fixed-height, so a row that grows when selected would desync it.
// The composer opens in a separate panel instead of inside the row.
const ROW_HEIGHT = 40;

export type AccountPickerProps = {
  accounts: Account[];
  selected: Record<string, boolean>;
  onToggle: (id: string) => void;
  onSelectAll: (ids: string[]) => void;
  onClear: () => void;
  query: string;
  onQuery: (q: string) => void;
  /** Comment composers, shown only for the flows that post text. */
  text: Record<string, string>;
  onText: (id: string, value: string) => void;
  textPlaceholder?: string | undefined;
  maxBatch: number;
  maxTextLen: number;
};

export function AccountPicker({
  accounts,
  selected,
  onToggle,
  onSelectAll,
  onClear,
  query,
  onQuery,
  text,
  onText,
  textPlaceholder,
  maxBatch,
  maxTextLen,
}: AccountPickerProps) {
  const selectedIds = useMemo(
    () => accounts.filter((a) => selected[a.id]).map((a) => a.id),
    [accounts, selected],
  );

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return accounts;
    return accounts.filter(
      (a) =>
        a.username.toLowerCase().includes(q) ||
        (a.location ?? '').toLowerCase().includes(q),
    );
  }, [accounts, query]);

  const { ref, slice, offset, total } = useAccountWindow(filtered.length);

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute left-2 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => onQuery(e.target.value)}
            placeholder="Search by username or city"
            className="pl-8"
            aria-label="Search accounts"
          />
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onSelectAll(filtered.slice(0, maxBatch).map((a) => a.id))}
          disabled={filtered.length === 0}
        >
          Select first {Math.min(filtered.length, maxBatch)}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onClear} disabled={selectedIds.length === 0}>
          Clear
        </Button>
      </div>

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>
          {filtered.length === accounts.length
            ? `${accounts.length} authenticated`
            : `${filtered.length} of ${accounts.length} match`}
        </span>
        <span>
          <span className="font-medium text-foreground">{selectedIds.length}</span> / {maxBatch} selected
        </span>
      </div>

      {filtered.length === 0 ? (
        <p className="rounded-md border border-border/60 p-3 text-sm text-muted-foreground">
          No authenticated account matches. Accounts finish a login on the Accounts page; only
          those can act.
        </p>
      ) : (
        <div
          ref={ref}
          className="max-h-72 overflow-y-auto rounded-md border border-border/60"
          role="listbox"
          aria-label="Authenticated accounts"
          aria-multiselectable
        >
          <div style={{ height: total, position: 'relative' }}>
            <div style={{ transform: `translateY(${offset}px)` }}>
              {slice.map((i) => {
                const a = filtered[i];
                if (!a) return null;
                const on = !!selected[a.id];
                const capped = !on && selectedIds.length >= maxBatch;
                return (
                  <label
                    key={a.id}
                    style={{ height: ROW_HEIGHT }}
                    className={`flex cursor-pointer items-center gap-2 border-b border-border/40 px-2 text-sm last:border-b-0 hover:bg-secondary/40 ${
                      capped ? 'opacity-50' : ''
                    }`}
                  >
                    <input
                      type="checkbox"
                      checked={on}
                      disabled={capped}
                      onChange={() => onToggle(a.id)}
                      aria-label={`Select @${a.username}`}
                    />
                    <span className="min-w-0 flex-1 truncate font-medium">@{a.username}</span>
                    {a.location ? (
                      <span className="hidden shrink-0 text-xs text-muted-foreground sm:inline">
                        {a.location}
                      </span>
                    ) : null}
                    <Badge variant="success" className="shrink-0">
                      ready
                    </Badge>
                  </label>
                );
              })}
            </div>
          </div>
        </div>
      )}

      {textPlaceholder && selectedIds.length > 0 ? (
        <div className="space-y-2 border-t border-border/60 pt-2">
          <p className="text-xs text-muted-foreground">
            Leave a body blank to use the template pool.
          </p>
          <div className="max-h-56 space-y-2 overflow-y-auto">
            {accounts
              .filter((a) => selected[a.id])
              .map((a) => (
                <div key={a.id} className="space-y-1">
                  <div className="flex items-center justify-between text-xs">
                    <span className="truncate font-medium">@{a.username}</span>
                    <span className="text-muted-foreground">
                      {(text[a.id] ?? '').length}/{maxTextLen}
                    </span>
                  </div>
                  <Textarea
                    rows={2}
                    maxLength={maxTextLen}
                    placeholder={textPlaceholder}
                    value={text[a.id] ?? ''}
                    onChange={(e) => onText(a.id, e.target.value)}
                  />
                </div>
              ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}

/** Fixed-height windowing over the filtered list. */
function useAccountWindow(count: number) {
  const { ref, slice: windowSlice } = useVirtualRowWindow(ROW_HEIGHT);
  const { start, end, before, after } = windowSlice(count);
  const indices = [];
  for (let i = start; i < end; i += 1) indices.push(i);
  return { ref, slice: indices, offset: before, total: before + (end - start) * ROW_HEIGHT + after };
}

export function PickerSummary({
  count,
  maxBatch,
  onClear,
}: {
  count: number;
  maxBatch: number;
  onClear: () => void;
}) {
  if (count === 0) return null;
  return (
    <div className="sticky bottom-0 z-10 flex items-center justify-between gap-2 border-t border-border/60 bg-background/95 px-2 py-2 backdrop-blur">
      <span className="text-sm">
        <span className="font-medium">{count}</span> account{count === 1 ? '' : 's'} selected
        {count > maxBatch ? ` — max ${maxBatch} per batch` : ''}
      </span>
      <Button type="button" variant="ghost" size="sm" onClick={onClear}>
        <X className="size-3.5" /> Clear
      </Button>
    </div>
  );
}
