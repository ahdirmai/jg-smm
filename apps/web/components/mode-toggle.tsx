/**
 * ModeToggle: the light/dark switch. The requirement is both themes must exist
 * and be switchable — this is the single control for it. next-themes persists
 * the choice and flips the `class` on <html> (theme-provider).
 *
 * Renders after mount only: the resolved theme is unknown during SSR, and an
 * early render would flash the wrong icon.
 *
 * `header` renders the segmented Light/Dark switch that sits in the top bar
 * (matches docs/prototype/styles.css `.theme-switch`); the default variant is
 * the full-width row used in the sidebar footer.
 */

'use client';

import { Moon, Sun } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTheme } from 'next-themes';

import { Button } from '@smm/ui';
import { cn } from '@/lib/utils';

export function ModeToggle({ header = false }: { header?: boolean }) {
  const { resolvedTheme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  useEffect(() => setMounted(true), []);

  if (!mounted) {
    return header ? <div className="size-9" aria-hidden /> : <div className="size-9" aria-hidden />;
  }

  const isDark = resolvedTheme === 'dark';

  if (header) {
    return (
      <div
        className="inline-flex items-center gap-0.5 rounded-full border bg-muted p-0.5"
        role="group"
        aria-label="Toggle theme"
      >
        <button
          type="button"
          aria-pressed={!isDark}
          onClick={() => setTheme('light')}
          className={cn(
            'inline-flex h-7 items-center gap-1.5 rounded-full px-2.5 text-xs font-medium transition-colors',
            !isDark
              ? 'bg-card text-foreground shadow-sm'
              : 'text-muted-foreground hover:text-foreground',
          )}
        >
          <Sun className="size-3.5" />
          Light
        </button>
        <button
          type="button"
          aria-pressed={isDark}
          onClick={() => setTheme('dark')}
          className={cn(
            'inline-flex h-7 items-center gap-1.5 rounded-full px-2.5 text-xs font-medium transition-colors',
            isDark
              ? 'bg-card text-foreground shadow-sm'
              : 'text-muted-foreground hover:text-foreground',
          )}
        >
          <Moon className="size-3.5" />
          Dark
        </button>
      </div>
    );
  }

  return (
    <Button
      variant="ghost"
      size="sm"
      className="w-full justify-start gap-3 px-3"
      onClick={() => setTheme(isDark ? 'light' : 'dark')}
      aria-label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
      title={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
    >
      {isDark ? <Sun className="size-4" /> : <Moon className="size-4" />}
      {isDark ? 'Light mode' : 'Dark mode'}
    </Button>
  );
}
