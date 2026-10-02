'use client';

import {
  AtSign,
  Facebook,
  Instagram,
  Linkedin,
  Music2,
  Twitter,
  Youtube,
  type LucideIcon,
} from 'lucide-react';

import type { Platform } from '@/lib/platforms';

/**
 * Per-platform glyph (DESIGN_SYSTEM principle 6: small monochrome icon in a
 * soft rounded square). lucide dropped brand marks for some networks, so the
 * fallback is the neutral @ rather than a missing icon.
 *
 * This module is client-only on purpose: the accounts table and the account
 * detail drawer both render the glyph, but `@/lib/platforms` stays
 * lucide-free because server components import it for a one-line label lookup.
 */
const PLATFORM_ICON: Record<Platform, LucideIcon> = {
  instagram: Instagram,
  threads: AtSign,
  facebook: Facebook,
  tiktok: Music2,
  linkedin: Linkedin,
  x: Twitter,
  youtube: Youtube,
};

export function platformIcon(platform: string): LucideIcon {
  return PLATFORM_ICON[platform as Platform] ?? AtSign;
}
