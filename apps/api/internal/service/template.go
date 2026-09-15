package service

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
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

// ScreenBanned reports whether the rendered text contains any word in the
// template's own denylist or the global team denylist. P3-03 wraps this into
// the screening pipeline; it lives on the service so the enqueue path calls
// one engine rather than a store plus a checker.
func ScreenBanned(text string, banned []string) bool {
	if len(banned) == 0 {
		return false
	}
	lowered := lowerASCII(text)
	for _, w := range banned {
		if w == "" {
			continue
		}
		if containsSubstr(lowered, lowerASCII(w)) {
			return true
		}
	}
	return false
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func containsSubstr(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
