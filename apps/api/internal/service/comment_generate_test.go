package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// capturingLLM is the minimal port.LLMCompleter for prompt-shaping tests: it
// records the user prompt and answers with one line per requested variant.
type capturingLLM struct {
	user   string
	budget int
}

func (c *capturingLLM) Available() bool { return true }

func (c *capturingLLM) Complete(_ context.Context, _, user string, _ float64, maxTokens int) (string, error) {
	c.user = user
	c.budget = maxTokens
	return "satu\ndua", nil
}

// The 1-5 intensity slider and the length setting must each steer the prompt:
// the low end goes gentle, the high end may push the edge, the middle stays
// balanced, and 0 (unset) defaults to the middle.
func TestGenerateIntensityAndLengthShapePrompt(t *testing.T) {
	cases := []struct {
		intensity CommentIntensity
		length    CommentLength
		want      string // substring that must appear in the user prompt
		absent    string // substring that must NOT appear
	}{
		{1, CommentLengthMedium, "gentle and agreeable", "pointed"},
		{2, CommentLengthMedium, "gentle and agreeable", "pointed"},
		{3, CommentLengthMedium, "balanced", "gentle and agreeable"},
		{4, CommentLengthMedium, "pointed", "gentle and agreeable"},
		{5, CommentLengthMedium, "pointed", "gentle and agreeable"},
		// Unset intensity defaults to the balanced middle.
		{0, CommentLengthMedium, "balanced", "gentle and agreeable"},
		{3, CommentLengthShort, "one sentence", "2-3 sentences"},
		{3, CommentLengthLong, "2-3 sentences", "one sentence"},
		// Unset length defaults to medium.
		{3, "", "1-2 sentences", "one sentence"},
	}
	for _, tc := range cases {
		c := &capturingLLM{}
		g := NewCommentGenerator(c, nil, nil)
		if _, err := g.Generate(context.Background(), GenerateInput{
			PostText:  "a post",
			Mode:      CommentModeSupport,
			Count:     2,
			Intensity: tc.intensity,
			Length:    tc.length,
		}); err != nil {
			t.Fatalf("Generate(intensity=%d, length=%q): %v", tc.intensity, tc.length, err)
		}
		if !strings.Contains(c.user, tc.want) {
			t.Errorf("intensity=%d length=%q: prompt missing %q\nprompt: %s", tc.intensity, tc.length, tc.want, c.user)
		}
		if tc.absent != "" && strings.Contains(c.user, tc.absent) {
			t.Errorf("intensity=%d length=%q: prompt must not contain %q\nprompt: %s", tc.intensity, tc.length, tc.absent, c.user)
		}
	}
}

// The default (intensity 0, length unset) must produce the balanced, medium,
// human-like prompt: a caller that sets nothing gets the canonical shape.
func TestGenerateDefaultPromptIsBalancedMediumHumanLike(t *testing.T) {
	c := &capturingLLM{}
	g := NewCommentGenerator(c, nil, nil)
	if _, err := g.Generate(context.Background(), GenerateInput{
		PostText: "a post",
		Mode:     CommentModeSupport,
		Count:    2,
	}); err != nil {
		t.Fatal(err)
	}
	want := "Write 2 distinct comment candidate(s) for the post above. Supportive: you agree with the post and amplify its point. Stay sincere, never sycophantic.\nLength: medium: 1-2 sentences. Rules: sound like a real person typing in a comment section"
	if !strings.Contains(c.user, want) {
		t.Errorf("default prompt drifted from the canonical shape:\n%s", c.user)
	}
	// The Indonesian language line must invite natural, human phrasing.
	if !strings.Contains(c.user, "casual and natural") {
		t.Errorf("prompt missing the human-like language line:\n%s", c.user)
	}
}

func TestGenerateRejectsOutOfRangeIntensity(t *testing.T) {
	g := NewCommentGenerator(&capturingLLM{}, nil, nil)
	for _, bad := range []CommentIntensity{-1, 6} {
		_, err := g.Generate(context.Background(), GenerateInput{
			PostText:  "a post",
			Mode:      CommentModeSupport,
			Count:     1,
			Intensity: bad,
		})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("intensity %d: want ErrValidation, got %v", bad, err)
		}
	}
}

func TestGenerateRejectsUnknownLength(t *testing.T) {
	g := NewCommentGenerator(&capturingLLM{}, nil, nil)
	_, err := g.Generate(context.Background(), GenerateInput{
		PostText: "a post",
		Mode:     CommentModeSupport,
		Count:    1,
		Length:   "gigantic",
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

// The completion budget must scale with the requested variant count: a large
// request (many long variants) can outgrow a flat budget and come back empty
// (finish_reason:length), which the caller would misread as a screening
// failure.
func TestTokenBudgetScalesWithCount(t *testing.T) {
	if tokenBudget(1) >= tokenBudget(10) {
		t.Fatalf("budget must grow with count: count=1 %d, count=10 %d", tokenBudget(1), tokenBudget(10))
	}
	if tokenBudget(10) >= tokenBudget(50) {
		t.Fatalf("budget must grow with count: count=10 %d, count=50 %d", tokenBudget(10), tokenBudget(50))
	}
	// The cap keeps a caller bug (or a future MaxAICommentVariants bump) from
	// asking the gateway for more than it accepts.
	if got := tokenBudget(100000); got != 16000 {
		t.Fatalf("tokenBudget(huge) = %d, want the 16000 cap", got)
	}
}

// fixedLLM answers with an exact string, to drive the output-side guards
// (dash normalisation, denylist, dedupe) that prompt shaping cannot test.
type fixedLLM struct {
	out string
}

func (f *fixedLLM) Available() bool { return true }
func (f *fixedLLM) Complete(_ context.Context, _, _ string, _ float64, _ int) (string, error) {
	return f.out, nil
}

// The generator must not ship em/en dashes, which read as machine-written.
// Two guards cover it: the prompt forbids them, and the parser rewrites any
// that still arrive. This pins both, so a later prompt edit cannot silently
// drop the rule the way the first version of the prompt did.
func TestGeneratePromptForbidsDashes(t *testing.T) {
	c := &capturingLLM{}
	g := NewCommentGenerator(c, nil, nil)
	if _, err := g.Generate(context.Background(), GenerateInput{
		PostText: "a post",
		Mode:     CommentModeSupport,
		Count:    1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"no em dashes", "no en dashes"} {
		if !strings.Contains(c.user, want) {
			t.Errorf("prompt missing %q:\n%s", want, c.user)
		}
	}
}

// Whatever the model emits, no shipped candidate may contain an em or en dash.
func TestGenerateNormalisesDashesInOutput(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		// The parenthetical aside the model reaches for becomes a comma.
		{"spaced em dash", "Janjinya banyak, tapi yang paling fake itu “nanti aku improve” — padahal cuma tar.", "Janjinya banyak, tapi yang paling fake itu “nanti aku improve”, padahal cuma tar."},
		{"spaced en dash", "Aku setuju – tapi pelan-pelan aja.", "Aku setuju, tapi pelan-pelan aja."},
		{"bare em dash", "Bagus—serius.", "Bagus, serius."},
		{"numeric range keeps a hyphen", "Sprint 2019—2021 chaos.", "Sprint 2019-2021 chaos."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewCommentGenerator(&fixedLLM{out: tc.out}, nil, nil)
			got, err := g.Generate(context.Background(), GenerateInput{
				PostText: "a post",
				Mode:     CommentModeSupport,
				Count:    1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("want 1 candidate, got %d: %v", len(got), got)
			}
			if got[0] != tc.want {
				t.Errorf("got  %q\nwant %q", got[0], tc.want)
			}
			if strings.ContainsAny(got[0], "—–") {
				t.Errorf("candidate still contains an em/en dash: %q", got[0])
			}
		})
	}
}
