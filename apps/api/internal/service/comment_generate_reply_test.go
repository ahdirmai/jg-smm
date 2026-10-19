package service

import (
	"context"
	"strings"
	"testing"
)

// A reply must be written as an ANSWER to one comment, not as a generic
// top-level comment dropped into a reply slot. What separates the two is the
// prompt: the caller names the comment, and the model is told to engage with it
// and to leave the handle out. Without a test the field can be added, wired,
// and silently ignored — the drafts still come back plausible.
func TestGenerateReplyContextShapesThePrompt(t *testing.T) {
	c := &capturingLLM{}
	g := NewCommentGenerator(c, nil, nil)
	if _, err := g.Generate(context.Background(), GenerateInput{
		PostText:     "janji tanpa roadmap tidak akan kejadian",
		AuthorHandle: "ahdirmai.id",
		ExistingComments: []string{
			"gw prefer no roadmap tapi sprint clear",
			"roadmap penting tapi execution yang beda dunia",
		},
		ReplyTo: &ReplyContext{
			Handle: "rackytampubolon",
			Text:   "Gw prefer no roadmap tapi sprint clear daripada roadmap indah",
		},
		Mode:  CommentModeSupport,
		Count: 2,
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// The target comment's BODY must be in the prompt, so the model can engage
	// with the actual argument rather than the thread's mood.
	if !strings.Contains(c.user, "Gw prefer no roadmap tapi sprint clear") {
		t.Errorf("prompt must quote the comment being answered\nprompt: %s", c.user)
	}
	// The handle identifies who is being answered.
	if !strings.Contains(c.user, "@rackytampubolon") {
		t.Errorf("prompt must name the handle being replied to\nprompt: %s", c.user)
	}
	// The ask must be a reply, not a comment.
	if !strings.Contains(c.user, "REPLYING") {
		t.Errorf("prompt must frame the task as a reply\nprompt: %s", c.user)
	}
	if !strings.Contains(c.user, "reply candidate") {
		t.Errorf("prompt must ask for replies\nprompt: %s", c.user)
	}
	// The model must be told NOT to emit the mention: the platform pre-fills it,
	// so a draft carrying "@handle" would double-tag (the worker APPENDS to the
	// prefilled mention, it does not replace it).
	if !strings.Contains(c.user, "Do NOT write their handle") {
		t.Errorf("prompt must forbid an @mention in the body\nprompt: %s", c.user)
	}
	// A top-level comment prompt must NOT claim to be a reply.
	if strings.Contains(c.user, "Write 2 distinct comment candidate") {
		t.Errorf("a reply must not keep the comment task wording\nprompt: %s", c.user)
	}
}

// The reverse guard: with no ReplyTo the prompt must stay the comment prompt.
// A reply-shaped ask leaking into the top-level flow would make every ordinary
// comment awkwardly address a stranger.
func TestGenerateWithoutReplyContextKeepsTheCommentPrompt(t *testing.T) {
	c := &capturingLLM{}
	g := NewCommentGenerator(c, nil, nil)
	if _, err := g.Generate(context.Background(), GenerateInput{
		PostText: "a post",
		Mode:     CommentModeSupport,
		Count:    1,
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(c.user, "Write 1 distinct comment candidate") {
		t.Errorf("a top-level comment must keep the comment task wording\nprompt: %s", c.user)
	}
	if strings.Contains(c.user, "REPLYING") {
		t.Errorf("a top-level comment must not be framed as a reply\nprompt: %s", c.user)
	}
}

// A reply whose target text is blank must fall back to the comment prompt
// rather than producing an empty "replying to \"\"" block: the UI can hold a
// stale id whose comment has no body, and the model would be answering nothing.
func TestGenerateReplyContextWithoutTextFallsBack(t *testing.T) {
	c := &capturingLLM{}
	g := NewCommentGenerator(c, nil, nil)
	if _, err := g.Generate(context.Background(), GenerateInput{
		PostText: "a post",
		ReplyTo:  &ReplyContext{Handle: "someone", Text: "   "},
		Mode:     CommentModeSupport,
		Count:    1,
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(c.user, "REPLYING") {
		t.Errorf("a blank target must not produce a reply prompt\nprompt: %s", c.user)
	}
	if !strings.Contains(c.user, "Write 1 distinct comment candidate") {
		t.Errorf("a blank target must fall back to the comment prompt\nprompt: %s", c.user)
	}
}

// The handle arrives with or without the leading @ depending on the caller;
// both must render as exactly one @.
func TestGenerateReplyContextNormalisesHandle(t *testing.T) {
	for _, handle := range []string{"someone", "@someone"} {
		c := &capturingLLM{}
		g := NewCommentGenerator(c, nil, nil)
		if _, err := g.Generate(context.Background(), GenerateInput{
			PostText: "a post",
			ReplyTo:  &ReplyContext{Handle: handle, Text: "a comment"},
			Mode:     CommentModeSupport,
			Count:    1,
		}); err != nil {
			t.Fatalf("Generate(handle=%q): %v", handle, err)
		}
		if strings.Contains(c.user, "@@someone") {
			t.Errorf("handle %q produced a double @\nprompt: %s", handle, c.user)
		}
		if !strings.Contains(c.user, "this specific comment by @someone") {
			t.Errorf("handle %q must render as @someone\nprompt: %s", handle, c.user)
		}
	}
}
