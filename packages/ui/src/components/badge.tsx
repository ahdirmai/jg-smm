import { cva, type VariantProps } from 'class-variance-authority';
import * as React from 'react';

import { cn } from '../lib/utils';

// DESIGN_SYSTEM §5.1: only success | warning | destructive | info | outline
// beyond the base default/secondary variants.
// Homies Lab: status pills are soft-tinted with a dark label, not a saturated
// fill (design principle 1 — one saturated hue, reserved for primary actions;
// a solid status fill would compete with the yellow accent).
const badgeVariants = cva(
  'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-primary text-primary-foreground',
        secondary: 'border-transparent bg-secondary text-secondary-foreground',
        outline: 'text-foreground',
        success:
          'border-transparent bg-[hsl(var(--success))]/12 text-[hsl(var(--success))]',
        warning: 'border-transparent bg-[hsl(var(--warning))]/16 text-foreground',
        info: 'border-transparent bg-[hsl(var(--info))]/12 text-foreground',
        destructive: 'border-transparent bg-destructive/12 text-destructive',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge, badgeVariants };
