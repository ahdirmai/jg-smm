'use client';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
  Label,
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
  Textarea,
} from '@smm/ui';
import { AlertCircle, MoreVertical, Pencil, Plus, Trash2 } from 'lucide-react';
import { useMemo, useState } from 'react';

import { useTemplates } from '@/lib/hooks/use-templates';
import { PLATFORM_LABEL, PLATFORMS } from '@/lib/platforms';
import type { CommentTemplate, CreateTemplateRequest } from '@/lib/api';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';

type Draft = {
  platform: CreateTemplateRequest['platform'];
  text: string;
  vars: string;
  weight: string;
  bannedWords: string;
  isActive: boolean;
};

function emptyDraft(): Draft {
  return {
    platform: 'instagram',
    text: '',
    vars: '',
    weight: '1',
    bannedWords: '',
    isActive: true,
  };
}

function templateToDraft(t: CommentTemplate): Draft {
  return {
    platform: t.platform,
    text: t.text,
    vars: t.vars.join(', '),
    weight: String(t.weight),
    bannedWords: (t.bannedWords ?? []).join(', '),
    isActive: t.isActive,
  };
}

function draftToRequest(d: Draft): CreateTemplateRequest {
  // vars/banned words are comma-separated in the UI; the API takes arrays.
  const split = (s: string) =>
    s
      .split(',')
      .map((p) => p.trim())
      .filter(Boolean);
  return {
    platform: d.platform,
    text: d.text,
    vars: split(d.vars),
    weight: Number(d.weight) || 1,
    bannedWords: split(d.bannedWords),
    isActive: d.isActive,
  };
}

export default function TemplatesPage() {
  const { templates, loading, error, create, update, remove } = useTemplates();
  const session = useSession();
  const role = session.status === 'authenticated' ? session.role : undefined;
  const canAct = can(role, 'act');
  const [platform, setPlatform] = useState<string>('all');
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<CommentTemplate | null>(null);
  const [draft, setDraft] = useState<Draft>(emptyDraft());
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const filtered = useMemo(
    () => (platform === 'all' ? templates : templates.filter((t) => t.platform === platform)),
    [templates, platform],
  );

  const startCreate = () => {
    setEditing(null);
    setDraft(emptyDraft());
    setFormError(null);
    setOpen(true);
  };

  const startEdit = (t: CommentTemplate) => {
    setEditing(t);
    setDraft(templateToDraft(t));
    setFormError(null);
    setOpen(true);
  };

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);

    if (!draft.text.trim()) {
      setFormError('A variant body is required.');
      return;
    }

    setBusy(true);
    try {
      const body = draftToRequest(draft);
      if (editing) {
        await update(editing.id, body);
      } else {
        await create(body);
      }
      setOpen(false);
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Save failed');
    } finally {
      setBusy(false);
    }
  };

  const onDelete = async (t: CommentTemplate) => {
    try {
      await remove(t.id);
    } catch (err) {
      console.error('template delete failed', err);
    }
  };

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <header className="flex items-end justify-between gap-4">
        <div className="space-y-1">
          <h1 className="text-3xl font-semibold tracking-tight">Templates</h1>
          <p className="text-sm text-muted-foreground">
            The comment pool. Text is composed from these variants and denylist-screened before a
            comment is queued.
          </p>
        </div>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button onClick={startCreate} disabled={!canAct}>
              <Plus />
              New variant
            </Button>
          </DialogTrigger>
          <DialogContent className="sm:max-w-[520px]">
            <DialogHeader>
              <DialogTitle>{editing ? 'Edit variant' : 'New variant'}</DialogTitle>
              <DialogDescription>
                {
                  '{topic} placeholders render from the target’s context. Every banned word must be a valid regex; a literal word is one.'
                }
              </DialogDescription>
            </DialogHeader>
            <form onSubmit={onSubmit} className="space-y-4">
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="tpl-platform">Platform</Label>
                  <Select
                    value={draft.platform}
                    onValueChange={(v) => setDraft({ ...draft, platform: v as Draft['platform'] })}
                  >
                    <SelectTrigger id="tpl-platform">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {PLATFORMS.map((p) => (
                        <SelectItem key={p} value={p}>
                          {PLATFORM_LABEL[p]}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="tpl-weight">Weight</Label>
                  <Input
                    id="tpl-weight"
                    type="number"
                    min={1}
                    value={draft.weight}
                    onChange={(e) => setDraft({ ...draft, weight: e.target.value })}
                  />
                </div>
              </div>

              <div className="space-y-2">
                <Label htmlFor="tpl-text">Body</Label>
                <Textarea
                  id="tpl-text"
                  value={draft.text}
                  onChange={(e) => setDraft({ ...draft, text: e.target.value })}
                  placeholder={'Loving the {topic} angle 🔥'}
                  rows={3}
                  disabled={busy}
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="tpl-vars">Variables</Label>
                <Input
                  id="tpl-vars"
                  value={draft.vars}
                  onChange={(e) => setDraft({ ...draft, vars: e.target.value })}
                  placeholder="topic, product"
                  disabled={busy}
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="tpl-banned">Banned words</Label>
                <Input
                  id="tpl-banned"
                  value={draft.bannedWords}
                  onChange={(e) => setDraft({ ...draft, bannedWords: e.target.value })}
                  placeholder="competitor1, \\bfree\\b"
                  disabled={busy}
                />
              </div>

              <div className="flex items-center gap-2">
                <Select
                  value={draft.isActive ? 'on' : 'off'}
                  onValueChange={(v) => setDraft({ ...draft, isActive: v === 'on' })}
                >
                  <SelectTrigger id="tpl-active" className="w-32">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="on">Active</SelectItem>
                    <SelectItem value="off">Paused</SelectItem>
                  </SelectContent>
                </Select>
                <Label htmlFor="tpl-active" className="text-sm text-muted-foreground">
                  Paused variants are never picked.
                </Label>
              </div>

              {formError ? (
                <div className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                  <AlertCircle className="size-4" />
                  {formError}
                </div>
              ) : null}

              <DialogFooter>
                <Button type="submit" disabled={busy}>
                  {editing ? 'Save changes' : 'Create variant'}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Variants</CardTitle>
          <CardDescription>
            {loading ? 'Loading…' : `${filtered.length} of ${templates.length} shown`}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <section className="flex items-center gap-2">
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
                  <TableHead>Platform</TableHead>
                  <TableHead>Body</TableHead>
                  <TableHead>Weight</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.length === 0 && !loading ? (
                  <TableRow>
                    <TableCell colSpan={5} className="py-10 text-center text-muted-foreground">
                      No variants yet. Create one to start the pool.
                    </TableCell>
                  </TableRow>
                ) : (
                  filtered.map((t) => (
                    <TableRow key={t.id}>
                      <TableCell>
                        <Badge variant="outline">{PLATFORM_LABEL[t.platform] ?? t.platform}</Badge>
                      </TableCell>
                      <TableCell className="max-w-[420px]">
                        <div className="truncate text-sm">{t.text}</div>
                        {t.vars.length > 0 ? (
                          <div className="text-xs text-muted-foreground">
                            {t.vars.map((v) => `{${v}}`).join(' ')}
                          </div>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-sm tabular-nums">{t.weight}</TableCell>
                      <TableCell>
                        <Badge variant={t.isActive ? 'success' : 'secondary'}>
                          {t.isActive ? 'Active' : 'Paused'}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="size-8"
                              disabled={!canAct}
                            >
                              <MoreVertical className="size-4" />
                              <span className="sr-only">Open variant menu</span>
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onClick={() => startEdit(t)}>
                              <Pencil />
                              Edit
                            </DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              className="text-destructive"
                              onClick={() => void onDelete(t)}
                            >
                              <Trash2 />
                              Delete
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
    </div>
  );
}
