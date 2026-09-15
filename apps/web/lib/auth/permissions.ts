/**
 * FE mirror of the BE permission model (`internal/domain/role.go`). The server
 * is the authority; this only hides/disables UI the caller cannot use, so a
 * stale mapping degrades to a blocked control, never an open one.
 */

import type { ApiSchemas } from '@smm/shared';

export type Role = ApiSchemas['Role'];

export const ROLES: Role[] = ['OWNER', 'STRATEGIST', 'OPERATOR', 'ANALYST'];

export const ROLE_LABEL: Record<Role, string> = {
  OWNER: 'Owner',
  STRATEGIST: 'Strategist',
  OPERATOR: 'Operator',
  ANALYST: 'Analyst',
};

export type Permission = 'read' | 'act' | 'export' | 'admin';

const ROLE_PERMISSIONS: Record<Role, Permission[]> = {
  // Owner is an implicit superset — the BE grants everything.
  OWNER: ['read', 'act', 'export', 'admin'],
  STRATEGIST: ['read'],
  OPERATOR: ['read', 'act'],
  ANALYST: ['read', 'export'],
};

export function can(role: Role | undefined, perm: Permission): boolean {
  if (!role) return false;
  return ROLE_PERMISSIONS[role].includes(perm);
}
