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
	user string
}

func (c *capturingLLM) Available() bool { return true }

func (c *capturingLLM) Complete(_ context.Context, _, user string, _ float64, _ int) (string, error) {
	c.user = user
	return "satu\ndua", nil
}

func TestGenerateIntensityShapesPrompt(t *testing.T) {
	cases := []struct {
		intensity CommentIntensity
		want      string // substring that must appear in the user prompt
		absent    string // substring that must NOT appear
	}{
		{CommentIntensitySoft, "safe and neutral", "slightly sarcastic"},
		{CommentIntensityMedium, "", "safe and neutral"},
		{CommentIntensityBold, "slightly sarcastic", "safe and neutral"},
		// Empty intensity = medium: byte-identical to the pre-intensity prompt.
		{"", "", "safe and neutral"},
	}
	for _, tc := range cases {
		c := &capturingLLM{}
		g := NewCommentGenerator(c, nil, nil)
		if _, err := g.Generate(context.Background(), GenerateInput{
			PostText:  "a post",
			Mode:      CommentModeSupport,
			Count:     2,
			Intensity: tc.intensity,
		}); err != nil {
			t.Fatalf("Generate(%q): %v", tc.intensity, err)
		}
		if tc.want != "" && !strings.Contains(c.user, tc.want) {
			t.Errorf("intensity %q: prompt missing %q\nprompt: %s", tc.intensity, tc.want, c.user)
		}
		if tc.absent != "" && strings.Contains(c.user, tc.absent) {
			t.Errorf("intensity %q: prompt must not contain %q\nprompt: %s", tc.intensity, tc.absent, c.user)
		}
	}
}

func TestGenerateIntensityMediumIsLegacyPrompt(t *testing.T) {
	// The default (medium) must be the exact pre-intensity prompt, so a
	// dashboard that never picks a level gets the old behaviour.
	c := &capturingLLM{}
	g := NewCommentGenerator(c, nil, nil)
	if _, err := g.Generate(context.Background(), GenerateInput{
		PostText: "a post",
		Mode:     CommentModeSupport,
		Count:    2,
	}); err != nil {
		t.Fatal(err)
	}
	want := "Write 2 distinct comment candidate(s) for the post above. Supportive: you agree with the post and amplify its point. Stay sincere, never sycophantic.\nRules: natural, human, 1-2 sentences each, no hashtags, no emojis, no quotes around the comment, no numbering. Reply with one comment per line and nothing else.\nWrite in Bahasa Indonesia, casual but polite."
	if !strings.Contains(c.user, want) {
		t.Errorf("medium prompt drifted from the legacy shape:\n%s", c.user)
	}
}

func TestGenerateRejectsUnknownIntensity(t *testing.T) {
	g := NewCommentGenerator(&capturingLLM{}, nil, nil)
	_, err := g.Generate(context.Background(), GenerateInput{
		PostText:  "a post",
		Mode:      CommentModeSupport,
		Count:     1,
		Intensity: "extreme",
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}
