package domain

import (
	"errors"
	"strings"
	"testing"
)

// P3-03 denylist primitives. Banned words are regexes, not literals, because
// the denylist has to express shapes (leetspeak, URL fragments, brand
// variants); a literal is still a valid regex, so the common case costs nothing.

func TestCompileBanned(t *testing.T) {
	t.Run("literals and regexes both compile", func(t *testing.T) {
		patterns, err := CompileBanned([]string{"spam", "c[0o]ke", `https?://`})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(patterns) != 3 {
			t.Fatalf("expected 3 patterns, got %d", len(patterns))
		}
	})

	t.Run("an empty list compiles to nothing, not nil-pointer risk", func(t *testing.T) {
		if patterns, err := CompileBanned(nil); err != nil || len(patterns) != 0 {
			t.Fatalf("expected (nil-or-empty, nil), got (%v, %v)", patterns, err)
		}
	})

	t.Run("a blank entry is skipped, not a panic", func(t *testing.T) {
		if patterns, err := CompileBanned([]string{"", "  ", "spam"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		} else if len(patterns) != 1 {
			t.Fatalf("expected the two blank entries dropped, got %d", len(patterns))
		}
	})

	t.Run("a malformed regex is rejected inline (validasi inline)", func(t *testing.T) {
		_, err := CompileBanned([]string{"good", "a(b"})
		if err == nil {
			t.Fatal("expected an error for an unbalanced group")
		}
		if !strings.Contains(err.Error(), "does not compile") {
			t.Fatalf("expected the message to name the failure, got %v", err)
		}
		if !strings.Contains(err.Error(), "a(b") {
			t.Fatalf("expected the message to quote the bad pattern, got %v", err)
		}
		// The wrapper keeps ErrValidation so a CRUD caller returns one 400,
		// not a 500: a typo in the denylist is a user error.
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("expected ErrValidation, got %v", err)
		}
	})
}

func TestMatchBanned(t *testing.T) {
	patterns, err := CompileBanned([]string{"spam", "c[0o]ke"})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	cases := []struct {
		name    string
		text    string
		wantHit bool
	}{
		{name: "exact literal", text: "this is spam", wantHit: true},
		{name: "case-insensitive (authored once)", text: "SPAM and Spam", wantHit: true},
		{name: "regex shape matches leetspeak", text: "c0ke zero", wantHit: true},
		{name: "regex shape matches the literal too", text: "coke zero", wantHit: true},
		{name: "clean text", text: "perfectly fine comment", wantHit: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, hit := MatchBanned(tc.text, patterns)
			if hit != tc.wantHit {
				t.Fatalf("MatchBanned(%q) hit = %v, want %v (pattern %q)", tc.text, hit, tc.wantHit, pattern)
			}
			// A hit must name the rule that fired: an operator cannot fix a
			// denylist from a message that does not say which rule matched.
			if hit && pattern == "" {
				t.Fatal("a hit must report the offending pattern")
			}
		})
	}

	t.Run("no patterns never flags anything", func(t *testing.T) {
		if _, hit := MatchBanned("anything", nil); hit {
			t.Fatal("an empty denylist must not flag text")
		}
	})
}

// TestValidateRejectsBadRegex proves the compile check fires at template edit
// time (inline), so a broken denylist is never saved.
func TestValidateRejectsBadRegex(t *testing.T) {
	good := CommentTemplate{Platform: PlatformInstagram, Text: "hi {topic}", Vars: []string{"topic"}, Weight: 1}
	if err := good.Validate(); err != nil {
		t.Fatalf("a clean template must validate, got %v", err)
	}

	bad := good
	bad.BannedWords = []string{"a(b"}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected a malformed denylist pattern to fail validation")
	} else if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}

	// A valid regex on the same template passes, so the check is not
	// "any banned word rejects".
	ok := good
	ok.BannedWords = []string{"c[0o]ke"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a compiling denylist must validate, got %v", err)
	}
}
