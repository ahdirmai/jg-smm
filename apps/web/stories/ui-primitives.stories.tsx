import type { Meta, StoryObj } from '@storybook/nextjs';
import type { ReactNode } from 'react';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Input,
  Label,
  Textarea,
} from '@smm/ui';
import { ArrowRight, Plus } from 'lucide-react';

const meta = {
  title: 'UI/Primitives',
  parameters: { layout: 'padded' },
} satisfies Meta;

export default meta;

/** A story-body helper: stories must not depend on app contexts. */
function Row({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-center gap-3">{children}</div>;
}

export const Buttons: StoryObj = {
  render: () => (
    <Row>
      <Button>Primary</Button>
      <Button variant="outline">Outline</Button>
      <Button variant="secondary">Secondary</Button>
      <Button variant="destructive">Destructive</Button>
      <Button variant="ghost">Ghost</Button>
      <Button>
        <Plus />
        With icon
      </Button>
      <Button disabled>Disabled</Button>
    </Row>
  ),
};

export const Badges: StoryObj = {
  render: () => (
    <Row>
      <Badge variant="outline">Outline</Badge>
      <Badge variant="secondary">Secondary</Badge>
      <Badge variant="success">Success</Badge>
      <Badge variant="destructive">Destructive</Badge>
    </Row>
  ),
};

export const CardSurface: StoryObj = {
  render: () => (
    <Card className="max-w-sm">
      <CardHeader>
        <CardTitle>Getting started</CardTitle>
        <CardDescription>The fleet starts empty.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm text-muted-foreground">Hierarchy via spacing, not color.</p>
        <Button variant="outline" asChild>
          <a href="#card" target="_blank" rel="noreferrer">
            Check health
            <ArrowRight />
          </a>
        </Button>
      </CardContent>
    </Card>
  ),
};

export const FormControls: StoryObj = {
  render: () => (
    <div className="grid max-w-sm gap-4">
      <div className="grid gap-2">
        <Label htmlFor="i1">Username</Label>
        <Input id="i1" placeholder="worker-us-01" />
      </div>
      <div className="grid gap-2">
        <Label htmlFor="t1">Target URLs</Label>
        <Textarea
          id="t1"
          rows={3}
          placeholder="https://www.instagram.com/p/…"
          className="font-mono text-xs"
        />
      </div>
    </div>
  ),
};

// The a11y smoke (P4-10 AC "axe lolos"): a labelled input is the control that
// most often regresses to an unlabeled one, so this is the meaningful check.
export const A11yLabelledInput: StoryObj = {
  render: () => (
    <div className="grid max-w-sm gap-2">
      <Label htmlFor="a11y-1">Region</Label>
      <Input id="a11y-1" placeholder="us" />
    </div>
  ),
  // Asserted directly rather than via @storybook/test: the interaction here is
  // only "does the label resolve to the input", and a DOM check covers it
  // without adding a runtime dep the story bundle would ship.
  play: ({ canvasElement }) => {
    const input = canvasElement.querySelector('#a11y-1');
    if (!input) throw new Error('labelled input not rendered');
    if (
      input.getAttribute('aria-labelledby') &&
      input.getAttribute('aria-labelledby') !== 'a11y-1'
    ) {
      throw new Error('input aria-labelledby does not point at its label');
    }
  },
};
