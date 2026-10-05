package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// CommentGenerator turns a scraped post (caption + existing comments) into
// per-account comment candidates. Two editorial modes, per the operator flow:
//
//	support  — agree with / amplify the post's framing
//	counter  — politely push back with a different angle
//
// Output is screened by the same denylist the template engine uses, so AI
// text can never ship words a human-authored template may not.
type CommentGenerator struct {
	llm port.LLMCompleter
	// banned are regex denylist patterns (domain.CompileBanned), shared with
	// the template engine's screen so AI text obeys the same rules.
	banned []*regexp.Regexp
}

// NewCommentGenerator wires the generator. A nil/unconfigured client keeps the
// type usable — Generate reports Unavailable and the UI hides the button.
// Patterns that fail to compile are dropped with a warning: a broken pattern
// must never un-screen the whole pool.
func NewCommentGenerator(client port.LLMCompleter, bannedPatterns []string, logger *slog.Logger) *CommentGenerator {
	if logger == nil {
		logger = slog.Default()
	}
	compiled, err := domain.CompileBanned(bannedPatterns)
	if err != nil {
		logger.Warn("comment generator: drop invalid banned pattern(s)", "err", err)
	}
	return &CommentGenerator{llm: client, banned: compiled}
}

// CommentMode is the editorial stance of the generated comment.
type CommentMode string

const (
	CommentModeSupport CommentMode = "support"
	CommentModeCounter CommentMode = "counter"
)

// Valid reports whether m is a known mode.
func (m CommentMode) Valid() bool {
	return m == CommentModeSupport || m == CommentModeCounter
}

// CommentIntensity is how hard the generated comment pushes. It is orthogonal
// to the stance (support/counter): the same stance can be written gently or
// sharply. It steers the prompt only — the denylist screen and the length
// cap apply to every level, so a "bold" draft can never ship a banned word.
type CommentIntensity string

const (
	CommentIntensitySoft   CommentIntensity = "soft"
	CommentIntensityMedium CommentIntensity = "medium"
	CommentIntensityBold   CommentIntensity = "bold"
)

// Valid reports whether i is a known intensity.
func (i CommentIntensity) Valid() bool {
	return i == CommentIntensitySoft || i == CommentIntensityMedium || i == CommentIntensityBold
}

// GenerateInput is one generation request: the post being replied to, what the
// crowd is saying (existing comments), and how many variants to produce.
type GenerateInput struct {
	// PostText is the target post's caption/body.
	PostText string
	// AuthorHandle is the post author's handle, for context only.
	AuthorHandle string
	// ExistingComments are up to ~10 scraped comments that set the tone.
	ExistingComments []string
	// Mode picks the editorial stance.
	Mode CommentMode
	// Count is how many variants to return. The dashboard asks for one per
	// selected account, so the ceiling is the batch cap (MAX_BATCH=50 in
	// apps/web/app/(dashboard)/actions/new-action-form.tsx) — anything lower
	// and a large batch silently reuses variants.
	Count int
	// Language steers the output language ("id" → Bahasa Indonesia, the
	// dashboard default).
	Language string
	// Intensity steers how hard the comment pushes (soft/medium/bold).
	// Empty means medium: the prompt is byte-identical to the pre-intensity
	// one, so a caller that does not pick a level gets exactly the old
	// behaviour.
	Intensity CommentIntensity
}

// Generate returns Count comment candidates. Every variant is distinct,
// screened, and within the platform comment length cap.
func (g *CommentGenerator) Generate(ctx context.Context, in GenerateInput) ([]string, error) {
	if !g.llm.Available() {
		return nil, fmt.Errorf("%w: AI generation is not configured", domain.ErrUnavailable)
	}
	if !in.Mode.Valid() {
		return nil, fmt.Errorf("%w: mode must be support or counter, got %q", domain.ErrValidation, in.Mode)
	}
	if in.Intensity != "" && !in.Intensity.Valid() {
		return nil, fmt.Errorf("%w: intensity must be soft, medium or bold, got %q", domain.ErrValidation, in.Intensity)
	}
	if in.Count < 1 {
		in.Count = 1
	}
	if in.Count > MaxAICommentVariants {
		in.Count = MaxAICommentVariants
	}

	lang := in.Language
	if lang == "" {
		lang = "id"
	}
	stance := "Supportive: you agree with the post and amplify its point. Stay sincere, never sycophantic."
	if in.Mode == CommentModeCounter {
		stance = "Counter: you respectfully disagree or add a different angle. Critique the idea, never the person. Stay civil."
	}
	comments := strings.TrimSpace(strings.Join(prefixEach(in.ExistingComments, "- "), "\n"))
	user := fmt.Sprintf(
		"Post%s:\n%s\n\nExisting comments:\n%s\n\nWrite %d distinct comment candidate(s) for the post above. %s\nRules: natural, human, 1-2 sentences each, no hashtags, no emojis, no quotes around the comment, no numbering. Reply with one comment per line and nothing else.",
		authorSuffix(in.AuthorHandle),
		truncateText(strings.TrimSpace(in.PostText), 1200),
		truncateText(comments, 800),
		in.Count,
		stance,
	)
	if lang == "id" {
		user += "\nWrite in Bahasa Indonesia, casual but polite."
	}
	switch in.Intensity {
	case CommentIntensitySoft:
		user += "\nKeep it safe and neutral: no sarcasm, no edge, nothing that could read as provocation."
	case CommentIntensityBold:
		user += "\nYou may be pointed, slightly sarcastic, and push the edge, but stay civil and never name-and-shame."
	}

	system := "You write short social-media comments for a social media management team. You never mention being an AI."
	out, err := g.llm.Complete(ctx, system, user, 0.9, tokenBudget(in.Count))
	if err != nil {
		return nil, err
	}

	candidates := make([]string, 0, in.Count)
	seen := map[string]struct{}{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, "\"•*- ")
		if line == "" || len(line) < 2 {
			continue
		}
		if len(line) > MaxCommentTextLen {
			line = line[:MaxCommentTextLen]
		}
		if _, hit := domain.MatchBanned(line, g.banned); hit {
			continue
		}
		lower := strings.ToLower(line)
		if _, dup := seen[lower]; dup {
			continue
		}
		seen[lower] = struct{}{}
		candidates = append(candidates, line)
		if len(candidates) == in.Count {
			break
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: AI output was empty or screened out", domain.ErrValidation)
	}
	return candidates, nil
}

func prefixEach(items []string, prefix string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, prefix+s)
		}
	}
	return out
}

func authorSuffix(handle string) string {
	if handle = strings.TrimSpace(handle); handle != "" {
		return " by @" + handle
	}
	return ""
}

// tokenBudget sizes the completion budget (max_tokens) for one generation.
// The configured gateway fronts a REASONING model that emits its thinking
// before its answer, and the thinking grows with how many variants are asked
// for: measured against the live gateway, one variant spends a few hundred
// tokens thinking, ten spend up to ~2000. A flat budget therefore runs out in
// the thinking on a large request and the answer comes back empty
// (`finish_reason: "length"`), which the caller would misread as a screening
// failure. The budget is a ceiling, not a target — the model stops early when
// it is done — so sizing it generously costs nothing for a small count and
// only saves the large ones. 500 + 300/variant leaves headroom over the
// measured tail; the adapter clamps it to the gateway's hard cap.
func tokenBudget(count int) int {
	budget := 500 + 300*count
	if budget > 16000 {
		budget = 16000
	}
	return budget
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
