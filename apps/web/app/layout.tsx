import type { Metadata } from 'next';

export const metadata: Metadata = {
  title: 'SMM Automation',
  description: 'Social Media Management — scrape, monitor, and act.',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
