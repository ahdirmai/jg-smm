'use client';

import { ThemeProvider as NextThemesProvider } from 'next-themes';

/**
 * Wraps the app with next-themes. Dark is the default (DESIGN_SYSTEM §3).
 */
export function ThemeProvider({ children }: { children: React.ReactNode }) {
  return (
    <NextThemesProvider attribute="class" defaultTheme="dark" enableSystem={false}>
      {children}
    </NextThemesProvider>
  );
}
