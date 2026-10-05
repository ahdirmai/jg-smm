# Actions: location grouping + comment intensity

**Date:** 2026-10-05
**Status:** implemented 2026-10-05

Two additions to the `/actions` page (operator requirement, agreed in chat):

1. **Group the New-Action account list by worker location.** The flat
   platform-account list is regrouped by the location (city) of the worker
   each account runs on, so an operator picking accounts for a batch sees
   "Jakarta (3) / Bandung (2)" instead of one undifferentiated stack.
2. **Comment intensity for AI generation.** The "AI generate" button gains a
   three-level intensity selector (soft / medium / bold), orthogonal to the
   existing support/counter stance: the same stance can be written gently or
   sharply.

## Design

### 1. Location grouping (FE only)

- Data source: the existing `GET /api/accounts/by-region` endpoint
  (`api.listAccountsByRegion` in `lib/api.ts`). It groups accounts by the
  worker's location (city), falling back to the worker's region; accounts
  with no worker land in `unassigned` (always sorted last). No BE change:
  the endpoint already returns exactly the grouping unit requested.
- `new-action-form.tsx` keeps its platform + status filter (ACTIVE/PAUSED on
  the scraped platform) but derives the list from the by-region response
  instead of the flat account list, and renders one section per non-empty
  group: a small uppercase group header (location name + count) over the
  existing account rows. Selection, per-account comment text, and submit are
  unchanged — grouping is presentation only.
- The form already holds a `useAccounts()` list for the account label
  fallback; the by-region fetch is a second, independent request (its own
  loading/error state, non-blocking: on failure the flat list is kept).

### 2. Intensity (BE + FE)

- `service.CommentIntensity` type: `soft` | `medium` | `bold` (empty =
  medium, unknown = 400 via `ErrValidation`). Added to `GenerateInput`.
- `http.generateCommentRequest` gains `Intensity string`; the handler maps
  it onto the service input.
- Prompt shaping: one sentence per level appended after the stance, e.g.
  soft = "Keep it safe and neutral: no sarcasm, no edge, nothing that could
  read as provocation."; bold = "You may be pointed, slightly sarcastic and
  push the edge, but stay civil and never name-and-shame." Medium adds
  nothing (the prompt is byte-identical to today's, so the default
  behaviour is unchanged).
- The denylist screen and the length cap apply to every level: intensity
  changes the prompt only, never the screening.
- FE: three segmented buttons (same visual pattern as the Comment/Like
  toggle) next to the AI-mode Select; default medium; the value rides on the
  `generateComment` request body.

## Files

- `apps/api/internal/service/comment_generate.go` — intensity type + prompt
- `apps/api/internal/http/scrape.go` — request field + mapping
- `apps/api/internal/service/comment_generate_test.go` — prompt shaping
  (new; fake `port.LLMCompleter` capturing the user prompt)
- `apps/web/lib/api.ts` — `generateComment` body gains `intensity`
- `apps/web/app/(dashboard)/actions/new-action-form.tsx` — grouping +
  segmented intensity control

## Verification

- `go test ./...` (API module), `pnpm typecheck`, `pnpm lint`.
- Browser smoke against the running stack: grouped list renders per
  location; intensity buttons switch; a real generate call (LLM configured
  in `.env`) returns text matching the chosen level.
