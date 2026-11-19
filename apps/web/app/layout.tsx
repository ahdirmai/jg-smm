import type { Metadata } from 'next';

import './globals.css';
import { ThemeProvider } from '@/components/theme-provider';
import { SessionProvider } from '@/components/session-provider';

export const metadata: Metadata = {
  title: 'SMM Automation',
  description: 'Social operations — scrape, plan, and act from one workspace.',
};

/**
 * Root layout is intentionally bare: fonts, theme, and session only. The
 * dashboard shell lives in app/(dashboard)/layout.tsx so standalone routes
 * (login) get their own chrome.
 */
export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html
      lang="en"
      // next-themes flips the class at runtime; a hardcoded `dark` here would
      // win on first paint and flash the wrong theme.
      suppressHydrationWarning
    >
      <body className="font-sans antialiased">
        <ThemeProvider>
          <SessionProvider>{children}</SessionProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
