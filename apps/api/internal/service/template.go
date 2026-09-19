package service

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// TemplateService (P3-02) is the comment engine: it picks one variant from the
// pool for a (platform, target) pair and renders it with the caller's var
// values. The two pool guarantees live at different layers:
//   - dedupe (7 days per target) is a store query — it is a property of the
//     pool, not of a row;
//   - weighted random is here, because it needs no SQL seed and is testable
//     with an injected rand.
type TemplateService struct {
	templates port.TemplateStore
	clock     port.Clock
	rng       *rand.Rand
	logger    *slog.Logger
}

// NewTemplateService wires the engine. rng may be nil (production seeds from
// the clock); tests inject one to make the pick deterministic.
func NewTemplateService(templates port.TemplateStore, clock port.Clock, rng *rand.Rand, logger *slog.Logger) *TemplateService {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = systemClock{}
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(clock.Now().UnixNano()))
	}
	return &TemplateService{templates: templates, clock: clock, rng: rng, logger: logger}
}

// PickOutcome is one rendered comment: the variant chosen, its id (so the
// action_job can link back to it), and the final text a worker posts.
type PickOutcome struct {
	TemplateID   string
	RenderedText string
}

// Pick selects one unused-active template for the target and renders it.
// It returns ErrTemplatePoolEmpty when every active variant has already been
// used against this target inside 7 days — the honest caller then skips the
// comment instead of repeating a variant, because a repeated comment looks
// bot-like and is what dedupe exists to prevent.
func (s *TemplateService) Pick(ctx context.Context, platform domain.Platform, targetID string, values map[string]string) (PickOutcome, error) {
	candidates, err := s.templates.PickForTarget(ctx, platform, targetID)
	if err != nil {
		return PickOutcome{}, fmt.Errorf("service.template.Pick: %w", err)
	}
	if len(candidates) == 0 {
		return PickOutcome{}, domain.ErrTemplatePoolEmpty
	}
	t := s.weightedPick(candidates)
	text := t.Render(values)
	if !domain.Rendered(text) {
		// A placeholder survived: a declared var had no value. Never enqueue a
		// half-rendered comment — a worker posting "{topic}" is worse than
		// posting nothing.
		return PickOutcome{}, fmt.Errorf("%w: template %s left an unresolved placeholder in %q", domain.ErrValidation, t.ID, text)
	}
	// Denylist screen (P3-03): a banned comment is rejected here, before it can
	// reach a worker queue. The patterns were compile-checked when the template
	// was saved, so a compile error now is only a lost race with an edit — and
	// the safe read then is to reject the comment rather than ship an unscreened
	// one. The error names the pattern that fired: an operator staring at
	// "rejected" with no rule cannot fix the denylist.
	if patterns, err := domain.CompileBanned(t.BannedWords); err == nil {
		if pattern, hit := domain.MatchBanned(text, patterns); hit {
			return PickOutcome{}, fmt.Errorf("%w: template %s denylist pattern %q matched %q", domain.ErrBannedPattern, t.ID, pattern, text)
		}
	}
	return PickOutcome{TemplateID: t.ID, RenderedText: text}, nil
}

// weightedPick draws one template with probability proportional to its weight.
// Weight 3 is three times as likely as weight 1; a single-candidate pool is a
// certainty, which is what makes the common case (one good variant) linear.
func (s *TemplateService) weightedPick(candidates []domain.CommentTemplate) domain.CommentTemplate {
	total := 0
	for _, c := range candidates {
		total += c.Weight
	}
	// rand.Intn(total) is uniform over [0,total); walking the weights maps that
	// draw onto the template whose slice it lands in.
	draw := s.rng.Intn(total)
	for _, c := range candidates {
		draw -= c.Weight
		if draw < 0 {
			return c
		}
	}
	// Unreachable when weights are positive (the schema guarantees > 0); the
	// guard is only for a zero-weight candidate set, which cannot reach here.
	return candidates[len(candidates)-1]
}

// --- CRUD surface (P3-13 template composer) --------------------------------

// The store runs Validate() on every write, so these methods are pass-through
// by design: the composer's contract (vars match the text, weight positive,
// denylist compiles) is enforced at the store boundary, one place, and the
// handler only shapes the request. No method invents defaults the schema
// already owns.

// Create adds one variant to the pool.
func (s *TemplateService) Create(ctx context.Context, t domain.CommentTemplate) (domain.CommentTemplate, error) {
	out, err := s.templates.CreateTemplate(ctx, t)
	if err != nil {
		return out, fmt.Errorf("service.template.Create: %w", err)
	}
	return out, nil
}

// Update replaces a variant's mutable fields.
func (s *TemplateService) Update(ctx context.Context, t domain.CommentTemplate) (domain.CommentTemplate, error) {
	out, err := s.templates.UpdateTemplate(ctx, t)
	if err != nil {
		return out, fmt.Errorf("service.template.Update: %w", err)
	}
	return out, nil
}

// Delete removes a variant from the pool. Past action_logs keep their
// template_id: the audit trail does not follow the pool's current membership.
func (s *TemplateService) Delete(ctx context.Context, id string) error {
	if err := s.templates.DeleteTemplate(ctx, id); err != nil {
		return fmt.Errorf("service.template.Delete: %w", err)
	}
	return nil
}

// List returns the pool. An empty platform lists every platform's variants —
// the dashboard's default view when no platform filter is applied; a concrete
// platform scopes the listing. The composer's Pick stays platform-scoped
// regardless (see PickForTarget): this only widens the read-only listing so a
// non-Instagram variant is visible in the UI and the platform filter has
// something to filter.
func (s *TemplateService) List(ctx context.Context, platform domain.Platform, includeInactive bool) ([]domain.CommentTemplate, error) {
	out, err := s.templates.ListTemplates(ctx, platform, includeInactive, &listPageSize, nil)
	if err != nil {
		return nil, fmt.Errorf("service.template.List: %w", err)
	}
	return out, nil
}

// listPageSize bounds the pool read. The pool is a curated set, not a feed: a
// large number here means the pool needs pruning, not a bigger page.
var listPageSize = 200
