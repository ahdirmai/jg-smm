import * as React from 'react';

import { cn } from '../lib/utils';

// shadcn/ui Input (Tailwind v4 tokens).
const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<'input'>>(
  ({ className, type, ...props }, ref) => {
    return (
      <input
        type={type}
        className={cn(
          'flex h-11 w-full rounded-[12px] border border-border/80 bg-white/45 px-3.5 py-1 text-base text-foreground shadow-[inset_0_1px_0_rgb(255_255_255/0.5)] transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/25 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-white/5 dark:shadow-none md:text-sm',
          className,
        )}
        ref={ref}
        {...props}
      />
    );
  },
);
Input.displayName = 'Input';

export { Input };
