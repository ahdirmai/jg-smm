'use client';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@smm/ui';
import { AlertCircle, MoreVertical, Pause, Play, Trash2, UserPlus } from 'lucide-react';
import { useMemo, useState } from 'react';

import { useAccounts } from '@/lib/hooks/use-accounts';
import type { Account } from '@/lib/api';
import { AddAccountDialog } from '@/components/add-account-dialog';

const PLATFORMS = ['INSTAGRAM', 'THREADS', 'FACEBOOK', 'TIKTOK', 'LINKEDIN', 'X', 'YOUTUBE'] as const;
type Platform = (typeof PLATFORMS)[number];

const PLATFORM_LABEL: Record<Platform, string> = {
  INSTAGRAM: 'Instagram',
  THREADS: 'Threads',
  FACEBOOK: 'Facebook',
  TIKTOK: 'TikTok',
  LINKEDIN: 'LinkedIn',
  X: 'X',
  YOUTUBE: 'YouTube',
};

const STATUSES = ['PENDING', 'ACTIVE', 'PAUSED', 'QUARANTINED', 'DEAD', 'ARCHIVED'] as const;

function statusTone(status: Account['status']): 'success' | 'outline' | 'secondary' | 'destructive' {
  switch (status) {
    case 'ACTIVE':
      return 'success';
    case 'PAUSED':
      return 'secondary';
    case 'ARCHIVED':
    case 'DEAD':
      return 'destructive';
    default:
      return 'outline';
  }
}

function authTone(status: Account['authStatus']): 'success' | 'outline' | 'destructive' {
  switch (status) {
    case 'AUTHENTICATED':
      return 'success';
    case 'FAILED':
      return 'destructive';
    default:
      return 'outline';
  }
}

export default function AccountsPage() {
  const { accounts, loading, error, setStatus, remove } = useAccounts();
  const [addOpen, setAddOpen] = useState(false);
  const [platform, setPlatform] = useState<string>('all');
  const [status, setStatusFilter] = useState<string>('all');
  const [busy, setBusy] = useState<string | null>(null);

  const filtered = useMemo(() => {
    return accounts.filter((a) => {
      if (platform !== 'all' && a.platform !== platform) return false;
      if (status !== 'all' && a.status !== status) return false;
      return true;
    });
  }, [accounts, platform, status]);

  const onAct = async (id: string, fn: (id: string) => Promise<void>) => {
    setBusy(id);
    try {
      await fn(id);
    } catch (err) {
      // Keep the table usable; the SSE refresh restores truth on the next tick.
      console.error('account op failed', err);
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <header className="flex items-end justify-between gap-4">
        <div className="space-y-1">
          <h1 className="text-3xl font-semibold tracking-tight">Accounts</h1>
          <p className="text-sm text-muted-foreground">
            Worker accounts used for actions. One account per platform per container.
          </p>
        </div>
        <Button onClick={() => setAddOpen(true)}>
          <UserPlus />
          Add account
        </Button>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Fleet accounts</CardTitle>
          <CardDescription>
            {loading ? 'Loading…' : `${filtered.length} of ${accounts.length} shown`}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <section className="flex flex-wrap items-center gap-2">
            <Select value={platform} onValueChange={setPlatform}>
              <SelectTrigger className="w-40">
                <SelectValue placeholder="All platforms" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All platforms</SelectItem>
                {PLATFORMS.map((p) => (
                  <SelectItem key={p} value={p}>
                    {PLATFORM_LABEL[p]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>

            <Select value={status} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-40">
                <SelectValue placeholder="All statuses" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All statuses</SelectItem>
                {STATUSES.map((s) => (
                  <SelectItem key={s} value={s}>
                    {s[0] + s.slice(1).toLowerCase()}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </section>

          {error ? (
            <div className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              <AlertCircle className="size-4" />
              {error}
            </div>
          ) : null}

          <div className="overflow-hidden rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Account</TableHead>
                  <TableHead>Platform</TableHead>
                  <TableHead>Container</TableHead>
                  <TableHead>Auth</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.length === 0 && !loading ? (
                  <TableRow>
                    <TableCell colSpan={6} className="py-10 text-center text-muted-foreground">
                      No accounts yet. Add one to provision a container.
                    </TableCell>
                  </TableRow>
                ) : (
                  filtered.map((a) => (
                    <TableRow key={a.id}>
                      <TableCell>
                        <div className="font-medium">@{a.username}</div>
                        {a.handle ? (
                          <div className="text-xs text-muted-foreground">{a.handle}</div>
                        ) : null}
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline">
                          {PLATFORM_LABEL[a.platform as Platform] ?? a.platform}
                        </Badge>
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {a.workerId ? a.workerId.slice(0, 12) : '—'}
                      </TableCell>
                      <TableCell>
                        <Badge variant={authTone(a.authStatus)}>{a.authStatus}</Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant={statusTone(a.status)}>{a.status}</Badge>
                      </TableCell>
                      <TableCell>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button variant="ghost" size="icon" disabled={busy === a.id}>
                              <MoreVertical />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuLabel>Account ops</DropdownMenuLabel>
                            <DropdownMenuSeparator />
                            {a.status === 'PAUSED' ? (
                              <DropdownMenuItem
                                onClick={() => void onAct(a.id, (id) => setStatus(id, 'ACTIVE'))}
                              >
                                <Play />
                                Resume
                              </DropdownMenuItem>
                            ) : (
                              <DropdownMenuItem
                                onClick={() => void onAct(a.id, (id) => setStatus(id, 'PAUSED'))}
                              >
                                <Pause />
                                Pause
                              </DropdownMenuItem>
                            )}
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              className="text-destructive focus:text-destructive"
                              onClick={() => void onAct(a.id, remove)}
                            >
                              <Trash2 />
                              Remove
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>

      <AddAccountDialog open={addOpen} onOpenChange={setAddOpen} />
    </div>
  );
}
