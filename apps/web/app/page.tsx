import { Button, Badge, Card, CardContent, CardDescription, CardHeader, CardTitle } from '@smm/ui';
import { ArrowRight, Plus } from 'lucide-react';

const PLATFORM_ROADMAP = [
  { name: 'Instagram', mvp: true },
  { name: 'Threads', mvp: true },
  { name: 'Facebook', mvp: false },
  { name: 'LinkedIn', mvp: false },
  { name: 'X', mvp: false },
  { name: 'YouTube', mvp: false },
  { name: 'TikTok', mvp: false },
];

export default function HomePage() {
  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <header className="flex items-end justify-between gap-4">
        <div className="space-y-1">
          <h1 className="text-3xl font-semibold tracking-tight">Dashboard</h1>
          <p className="text-sm text-muted-foreground">
            Scrape, monitor, and act across your social platforms.
          </p>
        </div>
        <Button>
          <Plus />
          Create container
        </Button>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Getting started</CardTitle>
          <CardDescription>
            The fleet starts empty. Create a container, then connect an account to it.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">
            This is the P0 walking skeleton — the dashboard shell, theme tokens and shared UI
            components. Feature screens land in later phases.
          </p>
          <Button variant="outline" asChild>
            <a href="http://localhost:24080/healthz" target="_blank" rel="noreferrer">
              Check API health
              <ArrowRight />
            </a>
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Platform roadmap</CardTitle>
          <CardDescription>Instagram and Threads are active in the MVP.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-2">
          {PLATFORM_ROADMAP.map((p) => (
            <Badge key={p.name} variant={p.mvp ? 'success' : 'outline'}>
              {p.name}
            </Badge>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
