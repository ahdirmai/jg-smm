package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// SentimentAnalyzer labels scraped text as POSITIVE / NEUTRAL / NEGATIVE.
//
// Why an LLM and not a word list: these are Indonesian Instagram comments. They
// are short, full of abbreviations ("gk", "bgt", "sih") and slang, and the
// interesting cases are sarcasm and backhanded agreement ("setuju, tapi...")
// — exactly what a lexicon gets wrong. The lexicons that ship for English do
// not cover the vocabulary at all.
//
// Cost control, in order of importance:
//  1. ONE call per batch, not one per comment. The model is asked for a single
//     JSON array; N comments cost one round trip.
//  2. The batch is capped (MaxSentimentBatch) so a post with hundreds of
//     comments cannot produce an unbounded prompt.
//  3. Each text is truncated: a label does not need the full body.
//
// A failure is never fatal to the scrape. Analysed sentiment is a convenience
// layer over content we already stored; if the model is down or unconfigured,
// the rows stay NULL ("not analysed") and the dashboard shows no badge. An
// ingest must not fail because a derived label could not be produced.
type SentimentAnalyzer struct {
	llm port.LLMCompleter
	log *slog.Logger
}

// MaxSentimentBatch bounds one call. 40 short comments fit comfortably in a
// single prompt while keeping the completion budget (and the failure blast
// radius) small.
const MaxSentimentBatch = 40

// maxSentimentTextLen truncates one item before it enters the prompt.
const maxSentimentTextLen = 400

// NewSentimentAnalyzer wires the analyzer. A nil logger falls back to the
// default; a nil/unconfigured LLM makes Analyze report unavailable and callers
// keep their rows unlabelled.
func NewSentimentAnalyzer(llm port.LLMCompleter, log *slog.Logger) *SentimentAnalyzer {
	if log == nil {
		log = slog.Default()
	}
	return &SentimentAnalyzer{llm: llm, log: log}
}

// Available reports whether analysis can be attempted at all. Safe on a nil
// receiver: an unconfigured deployment stores no SentimentAnalyzer, and every
// call site asks this before touching the LLM.
func (a *SentimentAnalyzer) Available() bool {
	return a != nil && a.llm != nil && a.llm.Available()
}

// SentimentResult is one label. Score is the model's own confidence and may be
// zero when it did not report one — callers store it as NULL in that case.
type SentimentResult struct {
	Label domain.Sentiment
	Score float64
	// HasScore distinguishes "the model said 0.0" from "the model said nothing",
	// which matters because the score column is nullable and 0.0 is a legal
	// (if unlikely) confidence.
	HasScore bool
}

// Analyze labels a batch of texts, returning a slice aligned 1:1 with the
// input. A text the model did not answer for comes back unlabelled rather than
// guessed: a wrong label on a real comment is worse than a missing one, because
// the operator acts on it.
//
// Order is preserved by construction: the prompt numbers the items and the
// parser reads the index back. A model that returns fewer rows than asked
// yields unlabelled tails, never shifted labels.
func (a *SentimentAnalyzer) Analyze(ctx context.Context, texts []string) ([]SentimentResult, error) {
	out := make([]SentimentResult, len(texts))
	if len(texts) == 0 {
		return out, nil
	}
	if !a.Available() {
		return out, fmt.Errorf("%w: sentiment analysis is not configured", domain.ErrUnavailable)
	}

	// Only the first MaxSentimentBatch are analysed; the tail stays unlabelled.
	// Slicing here (not in the caller) keeps the "one batch" contract local.
	limit := len(texts)
	if limit > MaxSentimentBatch {
		limit = MaxSentimentBatch
	}
	items := make([]string, 0, limit)
	for _, t := range texts[:limit] {
		items = append(items, truncateText(strings.TrimSpace(t), maxSentimentTextLen))
	}

	raw, err := a.llm.Complete(ctx, sentimentSystemPrompt, sentimentUserPrompt(items), 0, sentimentTokenBudget(limit))
	if err != nil {
		return out, err
	}
	labels, err := parseSentimentResponse(raw, limit)
	if err != nil {
		return out, err
	}
	for i, l := range labels {
		out[i] = l
	}
	return out, nil
}

const sentimentSystemPrompt = "You classify the sentiment of social-media comments. " +
	"You reply with JSON only, never prose, never an explanation. " +
	"You understand Indonesian, English and Indonesian internet slang, including abbreviations " +
	"('gk', 'nggak', 'bgt', 'sih'), and you detect sarcasm and backhanded agreement."

// sentimentUserPrompt builds the numbered batch. The numbering is what lets the
// parser realign a partial answer, so it is part of the wire format, not style.
func sentimentUserPrompt(items []string) string {
	var b strings.Builder
	b.WriteString("Classify each numbered comment below.\n")
	b.WriteString("Labels: POSITIVE (praise, agreement, support), NEGATIVE (criticism, complaint, mockery, sarcasm aimed at the subject), NEUTRAL (a question, an observation, or genuinely mixed with no dominant stance).\n")
	b.WriteString("Judge the stance toward the post's subject, not whether the person is polite.\n")
	b.WriteString(`Answer with a JSON array, one object per comment, in the same order: {"i":<number>,"label":"POSITIVE|NEUTRAL|NEGATIVE","score":<0..1>}.`)
	b.WriteString(" An empty or unreadable comment is NEUTRAL with a low score.")
	b.WriteString(" Return the array and nothing else.\n\n")
	for i, s := range items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	return b.String()
}

// sentimentTokenBudget sizes the completion for a batch. A reasoning model
// spends thinking tokens before the answer (see tokenBudget in the generator),
// so the budget grows with the item count and keeps headroom over the answer
// itself: each object is a few tokens, the array overhead is constant.
//
// maxSentimentTokens mirrors the adapter's hard cap (llm.maxTokensCap); asking
// for more is clamped down there anyway, and stopping here keeps the intent
// readable instead of relying on the adapter to trim it.
const maxSentimentTokens = 8192

func sentimentTokenBudget(n int) int {
	budget := 400 + 40*n
	if budget > maxSentimentTokens {
		return maxSentimentTokens
	}
	return budget
}

// sentimentAnswer is one element of the model's JSON array.
//
// Score is a POINTER so "absent" is distinguishable from 0.0. With a plain
// float64 the zero value makes a missing score look like a reported 0.0, and
// the row would store a confidence the model never gave — the exact
// "invented value" the nullable column exists to prevent.
type sentimentAnswer struct {
	I     int      `json:"i"`
	Label string   `json:"label"`
	Score *float64 `json:"score"`
}

var sentimentLabels = map[string]domain.Sentiment{
	"POSITIVE": domain.SentimentPositive,
	"NEUTRAL":  domain.SentimentNeutral,
	"NEGATIVE": domain.SentimentNegative,
}

// parseSentimentResponse decodes the model's array into exactly n results.
//
// It is deliberately forgiving about the envelope and strict about the values:
// the model often wraps JSON in a markdown fence or adds a sentence around it,
// so the array is located rather than assumed, but an unknown label is dropped
// instead of coerced. Guessing "NEUTRAL" for a label we did not understand
// would show the operator a confident verdict the model never gave.
func parseSentimentResponse(raw string, n int) ([]SentimentResult, error) {
	out := make([]SentimentResult, n)
	body := extractJSONArray(raw)
	if body == "" {
		return out, fmt.Errorf("%w: sentiment model returned no JSON array", domain.ErrValidation)
	}
	var answers []sentimentAnswer
	if err := json.Unmarshal([]byte(body), &answers); err != nil {
		return out, fmt.Errorf("%w: sentiment model returned unparseable JSON: %v", domain.ErrValidation, err)
	}
	labelled := 0
	for _, ans := range answers {
		idx := ans.I - 1
		if idx < 0 || idx >= n {
			continue // out-of-range index: ignore rather than panic
		}
		label, ok := sentimentLabels[strings.ToUpper(strings.TrimSpace(ans.Label))]
		if !ok {
			continue
		}
		res := SentimentResult{Label: label}
		// A score outside 0..1 is a bad write, not a judgement; the column has a
		// CHECK, so drop it rather than let the insert fail the whole batch. A
		// nil score is "not reported" and stays absent.
		if ans.Score != nil && *ans.Score >= 0 && *ans.Score <= 1 {
			res.Score, res.HasScore = *ans.Score, true
		}
		out[idx] = res
		labelled++
	}
	if labelled == 0 {
		return out, fmt.Errorf("%w: sentiment model returned no usable labels", domain.ErrValidation)
	}
	return out, nil
}

// extractJSONArray returns the outermost [...] substring, or "" when there is
// none. Models wrap JSON in ```json fences or a leading sentence often enough
// that requiring a clean body would reject valid answers.
func extractJSONArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}
