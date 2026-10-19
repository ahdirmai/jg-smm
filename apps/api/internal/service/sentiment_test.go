package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// stubLLM answers with a canned body and records the prompt it was given.
type stubLLM struct {
	reply   string
	err     error
	user    string
	system  string
	budget  int
	calls   int
	availab bool
}

func (s *stubLLM) Available() bool { return s.availab }

func (s *stubLLM) Complete(_ context.Context, system, user string, _ float64, maxTokens int) (string, error) {
	s.calls++
	s.system, s.user, s.budget = system, user, maxTokens
	if s.err != nil {
		return "", s.err
	}
	return s.reply, nil
}

func newStub(reply string) *stubLLM { return &stubLLM{reply: reply, availab: true} }

// The whole point of the batch design: N comments cost ONE call. A regression
// to per-comment calls would multiply the token spend by the comment count
// while every other assertion still passed.
func TestSentimentBatchIsOneCall(t *testing.T) {
	c := newStub(`[{"i":1,"label":"POSITIVE","score":0.9},{"i":2,"label":"NEGATIVE","score":0.8}]`)
	a := NewSentimentAnalyzer(c, nil)
	got, err := a.Analyze(context.Background(), []string{"bagus banget", "jelek sih ini"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if c.calls != 1 {
		t.Errorf("LLM calls = %d, want 1 (batch, not per-comment)", c.calls)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Label != domain.SentimentPositive || got[1].Label != domain.SentimentNegative {
		t.Errorf("labels = %v/%v, want POSITIVE/NEGATIVE", got[0].Label, got[1].Label)
	}
	if !got[0].HasScore || got[0].Score != 0.9 {
		t.Errorf("score = %v (has=%v), want 0.9", got[0].Score, got[0].HasScore)
	}
}

// Alignment is the failure that matters most: a shifted label silently
// attributes one person's sentiment to another's text, and nothing downstream
// can detect it. The model is asked to echo an index precisely so a short or
// reordered answer cannot shift the rest.
func TestSentimentAlignsByIndexNotPosition(t *testing.T) {
	// Deliberately out of order, and missing index 2 entirely.
	c := newStub(`[{"i":3,"label":"NEGATIVE","score":0.7},{"i":1,"label":"POSITIVE","score":0.6}]`)
	a := NewSentimentAnalyzer(c, nil)
	got, err := a.Analyze(context.Background(), []string{"satu", "dua", "tiga"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got[0].Label != domain.SentimentPositive {
		t.Errorf("slot 0 = %q, want POSITIVE (index 1)", got[0].Label)
	}
	if got[1].Label != "" {
		t.Errorf("slot 1 = %q, want empty (model skipped index 2)", got[1].Label)
	}
	if got[2].Label != domain.SentimentNegative {
		t.Errorf("slot 2 = %q, want NEGATIVE (index 3)", got[2].Label)
	}
}

// A label we do not recognise must be dropped, never coerced. Coercing to
// NEUTRAL would show the operator a confident verdict the model never gave.
func TestSentimentDropsUnknownLabels(t *testing.T) {
	c := newStub(`[{"i":1,"label":"MIXED","score":0.5},{"i":2,"label":"negative","score":0.4}]`)
	a := NewSentimentAnalyzer(c, nil)
	got, err := a.Analyze(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got[0].Label != "" {
		t.Errorf("unknown label must be dropped, got %q", got[0].Label)
	}
	// Case is not a value: lowercase "negative" is the same label.
	if got[1].Label != domain.SentimentNegative {
		t.Errorf("lowercase label = %q, want NEGATIVE", got[1].Label)
	}
}

// A score outside 0..1 must be dropped while the LABEL survives: the column has
// a CHECK constraint, and a bad score must not take a good verdict down with it.
func TestSentimentDropsOutOfRangeScoreKeepsLabel(t *testing.T) {
	c := newStub(`[{"i":1,"label":"POSITIVE","score":7},{"i":2,"label":"NEGATIVE"}]`)
	a := NewSentimentAnalyzer(c, nil)
	got, err := a.Analyze(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got[0].Label != domain.SentimentPositive {
		t.Errorf("label = %q, want POSITIVE despite the bad score", got[0].Label)
	}
	if got[0].HasScore {
		t.Errorf("score 7 must be dropped, got %v", got[0].Score)
	}
	// A missing score field is "no score", which is distinct from 0.0.
	if got[1].HasScore {
		t.Errorf("absent score must not become 0.0, got %v", got[1].Score)
	}
	if got[1].Label != domain.SentimentNegative {
		t.Errorf("label = %q, want NEGATIVE", got[1].Label)
	}
}

// Models wrap JSON in markdown fences or a sentence often enough that requiring
// a clean body would reject valid answers and leave every comment unlabelled.
func TestSentimentToleratesProseAndFences(t *testing.T) {
	for _, reply := range []string{
		"```json\n[{\"i\":1,\"label\":\"POSITIVE\",\"score\":0.9}]\n```",
		"Here you go:\n[{\"i\":1,\"label\":\"POSITIVE\",\"score\":0.9}]\nHope that helps!",
		"  [ {\"i\": 1, \"label\": \"POSITIVE\", \"score\": 0.9} ]  ",
	} {
		a := NewSentimentAnalyzer(newStub(reply), nil)
		got, err := a.Analyze(context.Background(), []string{"a"})
		if err != nil {
			t.Fatalf("Analyze(%q): %v", reply, err)
		}
		if got[0].Label != domain.SentimentPositive {
			t.Errorf("reply %q → label %q, want POSITIVE", reply, got[0].Label)
		}
	}
}

// An unusable answer is an error, not a silent all-empty success: the caller
// logs it and retries on the next scrape, whereas a quiet empty result would
// look like "nothing to label".
func TestSentimentErrorsOnUnusableAnswers(t *testing.T) {
	for _, reply := range []string{"", "I cannot help with that.", "[not json", `[{"i":1,"label":"??"}]`} {
		a := NewSentimentAnalyzer(newStub(reply), nil)
		if _, err := a.Analyze(context.Background(), []string{"a"}); err == nil {
			t.Errorf("reply %q must be an error", reply)
		}
	}
}

// A transport failure must surface, so the caller can log it and leave rows
// unlabelled rather than storing a guess.
func TestSentimentPropagatesLLMError(t *testing.T) {
	c := newStub("")
	c.err = errors.New("gateway down")
	a := NewSentimentAnalyzer(c, nil)
	if _, err := a.Analyze(context.Background(), []string{"a"}); err == nil {
		t.Fatal("a transport error must not be swallowed")
	}
}

// The batch cap bounds the prompt: a post with hundreds of comments must not
// produce an unbounded request. The tail stays unlabelled (empty), and the
// prompt itself carries no more than the cap.
func TestSentimentCapsBatchSize(t *testing.T) {
	c := newStub(`[]`)
	a := NewSentimentAnalyzer(c, nil)
	texts := make([]string, MaxSentimentBatch+25)
	for i := range texts {
		texts[i] = "c"
	}
	got, err := a.Analyze(context.Background(), texts)
	// A reply of "[]" has no usable labels, so an error is expected; the point
	// is what was SENT, not what came back.
	if err == nil {
		t.Log("unexpectedly accepted an empty label set")
	}
	if len(got) != len(texts) {
		t.Fatalf("len = %d, want %d (aligned with input)", len(got), len(texts))
	}
	if n := strings.Count(c.user, "\n1. "); n > 1 {
		t.Errorf("prompt numbered the batch %d times, want 1", n)
	}
	if !strings.Contains(c.user, "40. ") {
		t.Errorf("prompt must include item %d\nprompt: %s", MaxSentimentBatch, c.user)
	}
	if strings.Contains(c.user, "41. ") {
		t.Errorf("prompt must not exceed the %d-item cap", MaxSentimentBatch)
	}
}

// An unconfigured/unavailable LLM reports unavailable rather than answering
// empty: the caller uses this to skip the work entirely.
func TestSentimentUnavailable(t *testing.T) {
	var nilAnalyzer *SentimentAnalyzer
	if nilAnalyzer.Available() {
		t.Error("a nil analyzer must report unavailable")
	}
	if NewSentimentAnalyzer(nil, nil).Available() {
		t.Error("an analyzer with no client must report unavailable")
	}
	if NewSentimentAnalyzer(&stubLLM{availab: false}, nil).Available() {
		t.Error("an unavailable client must report unavailable")
	}
	if _, err := nilAnalyzer.Analyze(context.Background(), []string{"a"}); err == nil {
		t.Error("Analyze on a nil analyzer must error, not panic")
	}
	// An empty batch is a no-op, not an error: a post with no comments.
	got, err := NewSentimentAnalyzer(newStub("[]"), nil).Analyze(context.Background(), nil)
	if err != nil || len(got) != 0 {
		t.Errorf("empty batch: got %v, %v; want empty, nil", got, err)
	}
}

// The prompt must state the classification contract, not just list text: the
// labels are what the parser matches on, so a wording change that dropped them
// would make every answer unparseable.
func TestSentimentPromptStatesLabelsAndFormat(t *testing.T) {
	c := newStub(`[{"i":1,"label":"NEUTRAL","score":0.5}]`)
	a := NewSentimentAnalyzer(c, nil)
	if _, err := a.Analyze(context.Background(), []string{"apa ini?"}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	for _, want := range []string{"POSITIVE", "NEUTRAL", "NEGATIVE", `"i"`, `"label"`, "JSON array"} {
		if !strings.Contains(c.user, want) {
			t.Errorf("prompt missing %q\nprompt: %s", want, c.user)
		}
	}
	if !strings.Contains(c.system, "JSON") {
		t.Errorf("system prompt must pin the JSON-only contract, got: %s", c.system)
	}
	// The text itself must reach the prompt.
	if !strings.Contains(c.user, "apa ini?") {
		t.Errorf("prompt must carry the item text\nprompt: %s", c.user)
	}
}
