import type { Metadata } from 'next';
import { Inter, JetBrains_Mono } from 'next/font/google';

import './globals.css';
import { ThemeProvider } from '@/components/theme-provider';
import { DashboardShell } from '@/components/dashboard-shell';
import { SessionProvider } from '@/components/session-provider';

const inter = Inter({
  subsets: ['latin'],
  variable: '--font-inter',
  display: 'swap',
});

const jetbrainsMono = JetBrains_Mono({
  subsets: ['latin'],
  variable: '--font-jetbrains-mono',
  display: 'swap',
});

export const metadata: Metadata = {
  title: 'SMM Automation',
  description: 'Social Media Management — scrape, monitor, and act.',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html
      lang="en"
      suppressHydrationWarning
      className={`${inter.variable} ${jetbrainsMono.variable} dark`}
    >
      <body className="font-sans antialiased">
        <ThemeProvider>
          <SessionProvider>
            <DashboardShell>{children}</DashboardShell>
          </SessionProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
