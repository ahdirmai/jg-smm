'use client';

/**
 * Account picker for the action form.
 *
 * The fleet can hold hundreds of rows and only a fraction can act at any
 * moment, so this list shows ONLY authenticated accounts, makes search the
 * primary surface, and keeps the selected count visible while scrolling.
 *
 * The comment editor is a separate component: selection and writing are two
 * different decisions, and a textarea per selected account turned a 50-account
 * batch into a wall of forms. `CommentComposer` edits one account at a time and
 * can copy the current draft to the whole selection.
 */

import {
  Button,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
} from '@smm/ui';
import { Search } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';

import type { Account } from '@/lib/api';
import { useVirtualRowWindow } from '@/lib/hooks/use-virtual-window';

// One row height for every state: the window math is fixed-height, so a row
// that grows when selected would desync it.
const ROW_HEIGHT = 40;

export type AccountPickerProps = {
  accounts: Account[];
  selected: Record<string, boolean>;
  onToggle: (id: string) => void;
  onSelectAll: (ids: string[]) => void;
  onClear: () => void;
  query: string;
  onQuery: (q: string) => void;
  maxBatch: number;
};

export function AccountPicker({
  accounts,
  selected,
  onToggle,
  onSelectAll,
  onClear,
  query,
  onQuery,
  maxBatch,
}: AccountPickerProps) {
  const selectedIds = useMemo(
    () => accounts.filter((a) => selected[a.id]).map((a) => a.id),
    [accounts, selected],
  );

  // Cities come from the accounts that can actually act, so the menu never
  // offers a city with zero selectable accounts. Selection is not dropped when
  // the filter changes: filtering narrows the list, not the batch.
  const cities = useMemo(
    () =>
      Array.from(
        new Set(accounts.map((a) => a.location?.trim()).filter((v): v is string => !!v)),
      ).sort((a, b) => a.localeCompare(b)),
    [accounts],
  );
  const [city, setCity] = useState('all');
  const activeCity = city === 'all' || cities.includes(city) ? city : 'all';

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return accounts.filter((a) => {
      if (activeCity !== 'all' && (a.location ?? '') !== activeCity) return false;
      if (!q) return true;
      return (
        a.username.toLowerCase().includes(q) ||
        (a.location ?? '').toLowerCase().includes(q)
      );
    });
  }, [accounts, activeCity, query]);

  const { ref, slice, offset, total } = useAccountWindow(filtered.length);
  const overCap = selectedIds.length > maxBatch;

  return (
    <div className="space-y-2">
      <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_170px]">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => onQuery(e.target.value)}
            placeholder="Search by username or city"
            className="pl-9"
            aria-label="Search accounts"
            disabled={accounts.length === 0}
          />
        </div>
        <Select value={activeCity} onValueChange={setCity} disabled={accounts.length === 0}>
          <SelectTrigger aria-label="Filter accounts by city">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All cities</SelectItem>
            {cities.map((name) => (
              <SelectItem key={name} value={name}>
                {name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>
          {accounts.length} ready
          {filtered.length !== accounts.length ? ` · ${filtered.length} match` : ''}
          <span className="mx-1.5 text-border">·</span>
          <span className={overCap ? 'font-medium text-destructive' : 'font-medium text-foreground'}>
            {selectedIds.length}
          </span>{' '}
          selected
          <span className="text-muted-foreground"> · max {maxBatch}</span>
        </span>
        <div className="flex items-center gap-1">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => onSelectAll(filtered.slice(0, maxBatch).map((a) => a.id))}
            disabled={filtered.length === 0 || selectedIds.length >= maxBatch}
          >
            {filtered.length <= maxBatch ? `Select all ${filtered.length}` : `Select first ${maxBatch}`}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={onClear}
            disabled={selectedIds.length === 0}
          >
            Clear
          </Button>
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className="rounded-lg border border-border/60 p-3 text-sm text-muted-foreground">
          {accounts.length === 0
            ? 'No authenticated account on this platform. Finish a login on the Accounts page; only those can act.'
            : 'No account matches those filters. Try another city or username.'}
        </p>
      ) : (
        <div
          ref={ref}
          className="max-h-56 overflow-y-auto rounded-xl border border-border/60"
          role="group"
          aria-label="Authenticated accounts"
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
                    className={`flex cursor-pointer items-center gap-2.5 border-b border-border/40 px-2.5 text-sm last:border-b-0 hover:bg-secondary/40 ${
                      on ? 'bg-primary/5' : ''
                    } ${capped ? 'cursor-not-allowed opacity-50' : ''}`}
                  >
                    <input
                      type="checkbox"
                      checked={on}
                      disabled={capped}
                      onChange={() => onToggle(a.id)}
                      aria-label={`Select @${a.username}`}
                      className="size-4 shrink-0 accent-primary"
                    />
                    <span className="min-w-0 flex-1 truncate font-medium">@{a.username}</span>
                    {a.location ? (
                      <span className="hidden max-w-[42%] truncate text-xs text-muted-foreground sm:inline">
                        {a.location}
                      </span>
                    ) : null}
                  </label>
                );
              })}
            </div>
          </div>
        </div>
      )}
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

export type CommentComposerProps = {
  /** Only the selected accounts, in selection order. */
  accounts: Account[];
  text: Record<string, string>;
  onText: (id: string, value: string) => void;
  /** Copy the active draft to every selected account in one update. */
  onApplyAll: (value: string) => void;
  maxTextLen: number;
  placeholder: string;
};

export function CommentComposer({
  accounts,
  text,
  onText,
  onApplyAll,
  maxTextLen,
  placeholder,
}: CommentComposerProps) {
  const [activeId, setActiveId] = useState<string | null>(accounts[0]?.id ?? null);
  const [applied, setApplied] = useState(false);

  // Removing the active account must not leave the editor pointing at a row
  // that is no longer in the batch.
  useEffect(() => {
    setActiveId((current) =>
      current && accounts.some((a) => a.id === current) ? current : (accounts[0]?.id ?? null),
    );
  }, [accounts]);

  const active = accounts.find((a) => a.id === activeId) ?? accounts[0] ?? null;
  const activeText = active ? (text[active.id] ?? '') : '';

  useEffect(() => {
    setApplied(false);
  }, [activeId, activeText]);

  if (accounts.length === 0) {
    return (
      <p className="rounded-lg border border-dashed border-border/70 p-3 text-sm text-muted-foreground">
        Select at least one account above to write a comment.
      </p>
    );
  }

  const draftCount = accounts.filter((a) => (text[a.id] ?? '').trim().length > 0).length;
  const templateCount = accounts.length - draftCount;

  return (
    <div className="space-y-2.5">
      <div
        className="flex gap-1 overflow-x-auto pb-1 [scrollbar-width:thin]"
        role="group"
        aria-label="Choose an account to edit"
      >
        {accounts.map((a) => {
          const current = a.id === active?.id;
          const hasDraft = (text[a.id] ?? '').trim().length > 0;
          return (
            <button
              key={a.id}
              type="button"
              aria-pressed={current}
              onClick={() => setActiveId(a.id)}
              title={`@${a.username}: ${hasDraft ? 'custom draft' : 'template pool'}`}
              className={`flex shrink-0 items-center gap-1.5 rounded-lg border px-2 py-1.5 text-xs transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring ${
                current
                  ? 'border-primary/40 bg-primary/10 text-foreground'
                  : 'border-border/60 text-muted-foreground hover:bg-secondary/50 hover:text-foreground'
              }`}
            >
              <span
                className={`size-1.5 rounded-full ${hasDraft ? 'bg-success' : 'bg-border'}`}
                aria-label={hasDraft ? 'Custom draft set' : 'Uses template pool'}
              />
              <span className="max-w-[150px] truncate">@{a.username}</span>
            </button>
          );
        })}
      </div>

      <div>
        <div className="mb-1 flex items-center justify-between gap-2">
          <p className="truncate text-xs font-medium">@{active?.username}</p>
          <div className="flex shrink-0 items-center gap-2">
            {accounts.length > 1 ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-7 px-2 text-xs"
                onClick={() => {
                  onApplyAll(activeText);
                  setApplied(true);
                }}
              >
                Apply to all {accounts.length}
              </Button>
            ) : null}
            <span className="text-[11px] tabular-nums text-muted-foreground">
              {activeText.length}/{maxTextLen}
            </span>
          </div>
        </div>
        <Textarea
          rows={4}
          maxLength={maxTextLen}
          placeholder={placeholder}
          value={activeText}
          onChange={(e) => active && onText(active.id, e.target.value)}
          aria-label={`Comment for @${active?.username ?? 'selected account'}`}
        />
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted-foreground">
        <span>
          Blank uses the template pool. {draftCount} custom
          {templateCount > 0 ? ` · ${templateCount} template` : ''}.
        </span>
        <span role="status" className="text-success">
          {applied ? `Copied to ${accounts.length} accounts.` : ''}
        </span>
      </div>
    </div>
  );
}
