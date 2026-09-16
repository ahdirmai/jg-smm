'use client';

import { useCallback, useEffect, useState } from 'react';
import { Loader2, UserPlus, Users } from 'lucide-react';

import {
  Badge,
  Button,
  Card,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
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
} from '@smm/ui';
import { api, type User } from '@/lib/api';
import { ROLES, ROLE_LABEL, type Role } from '@/lib/auth/permissions';

/**
 * The team panel (P6-11). The MVP is single-team, so this is the whole roster.
 * Only an OWNER can create/edit/remove members — every write is re-checked
 * server-side, so this gating is UX, not a security boundary.
 */
export function TeamPanel({ admin }: { admin: boolean }) {
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [inviteOpen, setInviteOpen] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.listUsers();
      setUsers(res.users ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load team');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <Card>
      <div className="flex items-center justify-between border-b p-4">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <Users className="size-4" />
            Team
          </h2>
          <p className="text-xs text-muted-foreground">
            MVP is single-team. RBAC: owner · strategist · operator · analyst.
          </p>
        </div>
        {admin ? (
          <Button variant="outline" size="sm" onClick={() => setInviteOpen(true)}>
            <UserPlus className="size-4" />
            Invite member
          </Button>
        ) : (
          <Badge variant="outline">Owner-only</Badge>
        )}
      </div>

      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Member</TableHead>
              <TableHead>Role</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {users.map((u) => (
              <TeamRow
                key={u.id}
                user={u}
                admin={admin}
                onChanged={() => void load()}
                onError={setError}
              />
            ))}
            {users.length === 0 ? (
              <TableRow>
                <TableCell colSpan={4} className="py-10 text-center text-sm text-muted-foreground">
                  {loading ? 'Loading team…' : 'No members yet.'}
                </TableCell>
              </TableRow>
            ) : null}
          </TableBody>
        </Table>
      </div>

      {error ? (
        <p className="p-3 text-sm text-destructive" role="alert">
          {error}
        </p>
      ) : null}

      {admin ? (
        <InviteDialog
          open={inviteOpen}
          onOpenChange={setInviteOpen}
          onCreated={() => {
            void load();
            setInviteOpen(false);
          }}
          onError={setError}
        />
      ) : null}
    </Card>
  );
}

function TeamRow({
  user,
  admin,
  onChanged,
  onError,
}: {
  user: User;
  admin: boolean;
  onChanged: () => void;
  onError: (msg: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [role, setRole] = useState<Role>(user.role);

  const applyRole = async () => {
    if (role === user.role) return;
    setBusy(true);
    try {
      await api.updateUser(user.id, { role });
      onChanged();
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Failed to update role');
      setRole(user.role);
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      await api.removeUser(user.id);
      onChanged();
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Failed to remove member');
    } finally {
      setBusy(false);
    }
  };

  return (
    <TableRow>
      <TableCell>
        <div className="font-medium">{user.name}</div>
        <div className="text-xs text-muted-foreground">{user.email}</div>
      </TableCell>
      <TableCell>
        {admin ? (
          <Select value={role} onValueChange={(v) => setRole(v as Role)} disabled={busy}>
            <SelectTrigger className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {ROLES.map((r) => (
                <SelectItem key={r} value={r}>
                  {ROLE_LABEL[r]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Badge variant={user.role === 'OWNER' ? 'success' : 'info'}>
            {ROLE_LABEL[user.role]}
          </Badge>
        )}
      </TableCell>
      <TableCell className="text-right">
        <div className="flex justify-end gap-1">
          {admin && role !== user.role ? (
            <Button variant="outline" size="sm" onClick={() => void applyRole()} disabled={busy}>
              {busy ? <Loader2 className="animate-spin" /> : null}
              Save
            </Button>
          ) : null}
          {admin ? (
            <Button
              variant="ghost"
              size="sm"
              className="text-destructive"
              onClick={() => void remove()}
              disabled={busy}
            >
              Remove
            </Button>
          ) : null}
        </div>
      </TableCell>
    </TableRow>
  );
}

function InviteDialog({
  open,
  onOpenChange,
  onCreated,
  onError,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: () => void;
  onError: (msg: string) => void;
}) {
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<Role>('OPERATOR');
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    try {
      await api.createUser({ email, name, password, role });
      setEmail('');
      setName('');
      setPassword('');
      setRole('OPERATOR');
      onCreated();
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Failed to create member');
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Invite member</DialogTitle>
          <DialogDescription>
            The MVP has no email transport, so the provisional password is shown to you once — share
            it out of band.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="invite-email">Email</Label>
            <Input
              id="invite-email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="member@team.local"
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="invite-name">Name</Label>
            <Input
              id="invite-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Display name"
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="invite-password">Provisional password</Label>
            <Input
              id="invite-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="At least 8 characters"
            />
          </div>
          <div className="space-y-1">
            <Label>Role</Label>
            <Select value={role} onValueChange={(v) => setRole(v as Role)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ROLES.map((r) => (
                  <SelectItem key={r} value={r}>
                    {ROLE_LABEL[r]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            onClick={() => void submit()}
            disabled={busy || !email || !name || password.length < 8}
          >
            {busy ? <Loader2 className="animate-spin" /> : null}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
