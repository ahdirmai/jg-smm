/**
 * ModeToggle: the light/dark switch. The requirement is both themes must exist
 * and be switchable — this is the single control for it. next-themes persists
 * the choice and flips the `class` on <html> (theme-provider).
 *
 * Renders after mount only: the resolved theme is unknown during SSR, and an
 * early render would flash the wrong icon.
 */

'use client';

import { Moon, Sun } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTheme } from 'next-themes';

import { Button } from '@smm/ui';

export function ModeToggle() {
  const { resolvedTheme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  useEffect(() => setMounted(true), []);

  if (!mounted) {
    return <div className="size-9" aria-hidden />;
  }

  const isDark = resolvedTheme === 'dark';

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
