'use client';

import { Badge } from '@smm/ui';
import {
  AlertCircle,
  Archive,
  CheckCircle2,
  Clock,
  KeyRound,
  Pause,
  Play,
  Trash2,
  type LucideIcon,
} from 'lucide-react';

import type { Account } from '@/lib/api';

function statusTone(
  status: Account['status'],
): 'success' | 'outline' | 'secondary' | 'destructive' {
  switch (status) {
    case 'ACTIVE':
      // Enrolment, not liveness: an account stays ACTIVE while its container
      // is paused. Green here read as "this is running" and buried the
      // fleet's real PAUSED state; neutral keeps the operator's eye on the
      // worker-state badge that accompanies it.
      return 'outline';
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

/**
 * Lifecycle glyph, same rule as `AuthBadge` below: §10 gives a tinted pill a
 * glyph *and* a label, and the two pills sit side by side in the table — one
 * with an icon and one without reads as an oversight, not a distinction.
 *
 * Keyed by string on purpose: the generated schema lags the API enum
 * (`domain/account.go` also has PENDING and QUARANTINED), so keying this off
 * `Account['status']` would be a lie the first time the schema is regenerated.
 * An unknown status falls back to a neutral glyph.
 */
const STATUS_ICON: Record<string, LucideIcon> = {
  PENDING: Clock,
  ACTIVE: Play,
  PAUSED: Pause,
  QUARANTINED: AlertCircle,
  DEAD: Trash2,
  ARCHIVED: Archive,
};

/**
 * Auth pill. DESIGN_SYSTEM §10: a status colour never travels alone — every
 * tinted pill carries a glyph and a label, so the state survives a colourblind
 * read and a greyscale print.
 */
export function AuthBadge({ status }: { status: Account['authStatus'] }) {
  const Icon = status === 'AUTHENTICATED' ? CheckCircle2 : KeyRound;
  return (
    <Badge variant={authTone(status)}>
      <Icon className="size-3" />
      {status}
    </Badge>
  );
}

/** Lifecycle pill; carries `STATUS_ICON` for the same reason `AuthBadge` does. */
export function StatusBadge({ status }: { status: Account['status'] }) {
  const Icon = STATUS_ICON[status] ?? Clock;
  return (
    <Badge variant={statusTone(status)}>
      <Icon className="size-3" />
      {status}
    </Badge>
  );
}
