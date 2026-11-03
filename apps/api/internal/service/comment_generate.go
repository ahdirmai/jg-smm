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

// CommentIntensity is how hard the generated comment pushes, on a 1-5 scale
// the dashboard exposes as a slider. It is orthogonal to the stance
// (support/counter): the same stance can be written gently or sharply. It
// steers the prompt only — the denylist screen and the length cap apply to
// every level, so a "5" draft can never ship a banned word.
type CommentIntensity int

const (
	CommentIntensityMin    CommentIntensity = 1 // gentle, agreeable
	CommentIntensityMedium CommentIntensity = 3 // balanced, natural
	CommentIntensityMax    CommentIntensity = 5 // sharp, push the edge
)

// Valid reports whether i is a usable slider position.
func (i CommentIntensity) Valid() bool {
	return i >= CommentIntensityMin && i <= CommentIntensityMax
}

// CommentLength is how long each generated comment should be. It steers the
// prompt only; the platform length cap (MaxCommentTextLen) still applies.
type CommentLength string

const (
	CommentLengthShort  CommentLength = "short"
	CommentLengthMedium CommentLength = "medium"
	CommentLengthLong   CommentLength = "long"
)

// Valid reports whether l is a known length.
func (l CommentLength) Valid() bool {
	return l == CommentLengthShort || l == CommentLengthMedium || l == CommentLengthLong
}

// ReplyContext identifies the comment a reply answers. The handle matters as
// much as the text: the reply will tag that person (IG pre-fills the mention),
// so the draft must read as addressed to them and must not repeat their handle
// inside the body — the platform adds it.
type ReplyContext struct {
	// Handle is the commented author's handle, without the leading @.
	Handle string
	// Text is that comment's body.
	Text string
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
	// ReplyTo is the exact comment being answered, when the operator is in the
	// Reply flow. Non-empty switches the prompt from "write something on this
	// post" to "answer THIS person": the drafts then address the argument in
	// front of them instead of the thread's general mood. Empty for a top-level
	// comment, where there is nothing to answer.
	ReplyTo *ReplyContext
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
	// Intensity steers how hard the comment pushes, 1 (gentle) to 5 (sharp).
	// 0 means the dashboard default, 3 (balanced).
	Intensity CommentIntensity
	// Length steers how long each comment is (short/medium/long). Empty means
	// medium: a one-or-two sentence reply, the old behaviour.
	Length CommentLength
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
	if in.Intensity == 0 {
		in.Intensity = CommentIntensityMedium
	}
	if !in.Intensity.Valid() {
		return nil, fmt.Errorf("%w: intensity must be 1-5, got %d", domain.ErrValidation, in.Intensity)
	}
	if in.Length != "" && !in.Length.Valid() {
		return nil, fmt.Errorf("%w: length must be short, medium or long, got %q", domain.ErrValidation, in.Length)
	}
	if in.Length == "" {
		in.Length = CommentLengthMedium
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

	// A reply answers ONE comment, so the prompt is built around that comment
	// instead of the thread's general mood. The rest of the scraped comments
	// stay in as background (what the room is saying), but the ask changes from
	// "comment on this post" to "answer this person".
	task := fmt.Sprintf(
		"Write %d distinct comment candidate(s) for the post above. %s\nLength: %s. Rules: sound like a real person typing in a comment section, not a brand account: vary the wording so no two candidates start the same way, write naturally, no hashtags, no em dashes and no en dashes (never use the — or – character: use a comma, a colon or a full stop instead), no quotes around the comment, no numbering. Reply with one comment per line and nothing else.",
		in.Count, stance, lengthInstruction(in.Length),
	)
	target := ""
	if in.ReplyTo != nil && strings.TrimSpace(in.ReplyTo.Text) != "" {
		handle := strings.TrimPrefix(strings.TrimSpace(in.ReplyTo.Handle), "@")
		target = fmt.Sprintf(
			"\n\nYou are REPLYING to this specific comment by @%s:\n\"%s\"\n\nWrite %d repl(y/ies) that answer @%s directly: engage with the point they actually made, agree or push back on it. Do NOT write their handle or any @mention inside the text; the platform adds the tag automatically. Do not greet them by name. Do not repeat their comment back to them.",
			handle,
			truncateText(strings.TrimSpace(in.ReplyTo.Text), 600),
			in.Count,
			handle,
		)
		task = fmt.Sprintf(
			"Write %d distinct reply candidate(s) for the post above.%s\nLength: %s. Rules: sound like a real person typing in a comment section, not a brand account: vary the wording so no two candidates start the same way, write naturally, no hashtags, no em dashes and no en dashes (never use the — or – character: use a comma, a colon or a full stop instead), no quotes around the reply, no numbering. Reply with one reply per line and nothing else.",
			in.Count, target, lengthInstruction(in.Length),
		)
	}
	user := fmt.Sprintf(
		"Post%s:\n%s\n\nExisting comments:\n%s\n\n%s",
		authorSuffix(in.AuthorHandle),
		truncateText(strings.TrimSpace(in.PostText), 1200),
		truncateText(comments, 800),
		task,
	)
	if lang == "id" {
		user += "\nWrite in Bahasa Indonesia, casual and natural: everyday abbreviations and light slang are fine (e.g. 'nggak', 'bgt', 'sih', 'gimana', 'gw/aku' where it fits), but keep it readable."
	}
	user += "\n" + intensityInstruction(in.Intensity)

	system := "You write short social-media comments for a social media management team. " +
		"You write as a supporter of H. Isam, Jhonlin Group and its subsidiaries: when the " +
		"thread touches them or anything connected to them, you take their side, defend " +
		"their record and amplify the good they do, and you never attack, mock or imply " +
		"wrongdoing against them. You never mention being an AI. You never use em dashes " +
		"(—) or en dashes (–); use plain commas, colons or full stops instead."
	if relatesToPrincipal(in.PostText, in.ExistingComments) {
		system += " This post is about H. Isam, Jhonlin Group or an affiliate: the " +
			"supportive stance above is mandatory here, and any counter framing must stay " +
			"constructive and still stand with them."
	}
	out, err := g.llm.Complete(ctx, system, user, 0.9, tokenBudget(in.Count))
	if err != nil {
		return nil, err
	}

	candidates := make([]string, 0, in.Count)
	seen := map[string]struct{}{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, "\"•*- ")
		line = normaliseDashes(line)
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

// rePrincipal matches a mention of the principal (H. Isam, Jhonlin Group) or a
// named subsidiary anywhere in the thread. Matching is case-insensitive and
// tolerant of the ways a handle writes them ("h. isam", "h isam", "pak isam",
// "jhonlin", "jhonlin group", "pt jhonlin"). It gates the prompt's affiliation
// clause: the engine only forces the supporter stance on threads that actually
// concern them, so an unrelated post is untouched.
var rePrincipal = regexp.MustCompile(`(?i)\b(?:h\.?\s*isam|pak\s+isam|jhonlin|johnlin|jhon\s*lin)\b`)

// relatesToPrincipal reports whether any thread text mentions the principal.
// It scans the post body, the author handle and the scraped comments: a post
// that never names them but whose comment section is arguing about them is
// still a thread about them.
func relatesToPrincipal(postText string, comments []string) bool {
	if rePrincipal.MatchString(postText) {
		return true
	}
	for _, c := range comments {
		if rePrincipal.MatchString(c) {
			return true
		}
	}
	return false
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

// lengthInstruction turns the length setting into one prompt line.
func lengthInstruction(l CommentLength) string {
	switch l {
	case CommentLengthShort:
		return "short: one sentence, a few words, the way a quick reply reads"
	case CommentLengthLong:
		return "longer: 2-3 sentences, a real thought, still one comment"
	default:
		return "medium: 1-2 sentences"
	}
}

// intensityInstruction turns the 1-5 slider into one prompt line. The middle
// of the scale is deliberately unmarked: it is the natural default, so the
// prompt adds nothing there.
func intensityInstruction(i CommentIntensity) string {
	switch {
	case i <= 2:
		return "Keep it gentle and agreeable: no sarcasm, no edge, nothing that could read as provocation."
	case i >= 4:
		return "You may be pointed, a bit sarcastic, and push the edge, but stay civil and never name-and-shame."
	default:
		return "Keep it balanced: natural, a little opinion, no edge."
	}
}

// tokenBudget sizes the completion budget (max_tokens) for one generation.
// It is a ceiling, not a target — the model stops early when it is done — so
// sizing it generously costs nothing for a small count and only protects the
// large ones: 500 + 300/variant leaves headroom over the measured tail for a
// long-variant request, and the adapter clamps it to the gateway's hard cap.
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

// reDigitRange matches an em/en dash used as a numeric range (2019—2021),
// which wants a hyphen, not the comma the parenthetical case below uses.
var reDigitRange = regexp.MustCompile(`(\d)\s*[—–]\s*(\d)`)

// normaliseDashes rewrites em/en dashes the model still emits despite the
// prompt. It is the third layer of the guard: the user prompt forbids them,
// the system prompt repeats it, and the parser rewrites whatever slips
// through, so a later prompt tweak cannot silently let them back into shipped
// comments. A dash between digits is a range and becomes a hyphen; every other
// dash is a parenthetical aside and becomes a comma, which reads closest to the
// original and stays grammatical. Leading/trailing comma artifacts are
// trimmed, since the model sometimes opens or closes a line with the dash.
func normaliseDashes(s string) string {
	s = reDigitRange.ReplaceAllString(s, "$1-$2")
	s = strings.ReplaceAll(s, " — ", ", ")
	s = strings.ReplaceAll(s, " – ", ", ")
	s = strings.ReplaceAll(s, "—", ", ")
	s = strings.ReplaceAll(s, "–", ", ")
	s = strings.TrimPrefix(s, ", ")
	s = strings.TrimSuffix(s, ", ")
	return s
}
