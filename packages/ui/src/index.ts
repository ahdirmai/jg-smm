/**
 * shadcn/ui shared components (source lives here, copied in via the shadcn CLI
 * convention but kept in this package so apps share one implementation).
 */
export { cn } from './lib/utils';

export { Button, buttonVariants, type ButtonProps } from './components/button';
export {
  Card,
  CardHeader,
  CardFooter,
  CardTitle,
  CardDescription,
  CardContent,
} from './components/card';
export { Badge, badgeVariants, type BadgeProps } from './components/badge';
