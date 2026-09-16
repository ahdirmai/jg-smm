# 0007. UI built on shadcn/ui

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: frontend, design-system

## Context and Problem Statement

The operator dashboard must feel like an internal tool a social-media specialist uses for hours: accessible, themeable (light/dark), minimalist but professional. We do not want to hand-roll primitives nor adopt a heavy component kit with its own visual identity.

## Decision Drivers

- Own the code (copy-in primitives, no black-box upgrades).
- First-class Tailwind + Radix accessibility.
- Support light/dark tokens with an indigo accent.

## Considered Options

1. MUI / Chakra / Mantine.
2. Headless Radix + custom CSS from scratch.
3. shadcn/ui (Radix + Tailwind, copied in).

## Decision Outcome

Chosen option: **"shadcn/ui (Radix + Tailwind, copied in)."** because shadcn/ui gives accessible primitives as source we own, styled with our Tailwind tokens.

### Consequences

- **Good**: Full control, no version lock, consistent tokens across the app.
- **Bad**: We maintain the components ourselves.
- **Neutral**: Design tokens live in `apps/web`; shared primitives in `@smm/ui`.

### Confirmation

Every screen uses tokenised classes; light/dark verified by the prototype browser check.

## Pros and Cons of the Options

### shadcn/ui (Radix + Tailwind, copied in). *(chosen)*

- **Good**: Full control, no version lock, consistent tokens across the app.
- **Bad**: We maintain the components ourselves.

### Alternatives

- **MUI / Chakra / Mantine.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

- **Headless Radix + custom CSS from scratch.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- DESIGN_SYSTEM; P0-08; prototype `docs/prototype/`.
- See also: `../DEVELOPMENT_RULE.md` §17.1, `../PRD.md`, `../ERD.md`, `../SYSTEM_DESIGN.md`.
