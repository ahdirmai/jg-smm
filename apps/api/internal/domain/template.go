package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// CommentTemplate (P3-02) is one variant in the comment pool. A template is
// platform-scoped (IG and Threads differ in tone and length), declares the
// vars its text may reference as {topic}, carries a pick weight and a
// per-template denylist, and can be soft-disabled without deleting the rows
// that explain past action_logs.
type CommentTemplate struct {
	ID          string
	Platform    Platform
	Text        string
	Vars        []string
	Weight      int
	BannedWords []string
	IsActive    bool
	CreatedAt   time.Time
}

// TemplatePlaceholder is the brace form a declared var takes inside Text, e.g.
// {topic}. Centralised so the composer and the renderer agree on the shape
// without either parsing the other's strings.
const TemplatePlaceholder = "{%s}"

// Validate checks the invariants the schema cannot express because they span
// Text and Vars together:
//   - weight must be positive (the schema CHECK covers it too, but this gives
//     the composer a 400 before a DB round trip);
//   - text must be non-blank (schema CHECK covers it, same reason);
//   - every {var} referenced in the text must be declared, and every declared
//     var must appear at least once — a declared-but-unused var is a latent
//     typo that would silently render the raw placeholder to a worker;
//   - every banned-word entry must compile as a regex (P3-03), so a typo in
//     the denylist is caught at edit time, not the first time a comment is
//     about to ship.
func (t CommentTemplate) Validate() error {
	if strings.TrimSpace(t.Text) == "" {
		return fmt.Errorf("%w: template text must not be blank", ErrValidation)
	}
	if t.Weight <= 0 {
		return fmt.Errorf("%w: weight must be positive, got %d", ErrValidation, t.Weight)
	}
	if _, err := CompileBanned(t.BannedWords); err != nil {
		return err
	}
	declared := map[string]bool{}
	for _, v := range t.Vars {
		declared[v] = true
	}
	// Placeholder words inside the text: {topic}, {product}, {handle}.
	used := map[string]bool{}
	for _, word := range strings.Split(t.Text, "{") {
		if !strings.Contains(word, "}") {
			continue
		}
		name := strings.SplitN(word, "}", 2)[0]
		if name == "" {
			continue
		}
		used[name] = true
		if !declared[name] {
			return fmt.Errorf("%w: text references undeclared var {%s}", ErrValidation, name)
		}
	}
	for _, v := range t.Vars {
		if !used[v] {
			return fmt.Errorf("%w: declared var {%s} is not used by the text", ErrValidation, v)
		}
	}
	return nil
}

// Render substitutes vars into the text. A missing value leaves the raw
// placeholder so the caller can reject the result rather than post a
// half-rendered comment; Render itself never invents content.
func (t CommentTemplate) Render(values map[string]string) string {
	out := t.Text
	for _, name := range t.Vars {
		val, ok := values[name]
		if !ok || val == "" {
			// Leave the placeholder: the caller checks RenderedText for a "{"
			// before enqueuing, and a worker must never post "{topic}".
			continue
		}
		out = strings.ReplaceAll(out, fmt.Sprintf(TemplatePlaceholder, name), val)
	}
	return out
}

// Rendered reports whether the text still contains an unresolved placeholder.
// A worker must never post "{topic}", so the enqueue path rejects a comment
// whose rendering did not complete.
func Rendered(text string) bool { return !strings.ContainsAny(text, "{}") }

// ErrTemplatePoolEmpty signals the pick could not serve a template: the pool
// for the platform has no active template unused against this target in 7
// days. The caller's honest move is to skip the comment, not to repeat one.
var ErrTemplatePoolEmpty = errors.New("template pool empty: no unused active template for this target in 7 days")

// ErrBannedPattern signals a rendered comment matched a denylist pattern, so
// it must never reach a worker queue (P3-03). Not retryable: re-running the
// compose would draw the same banned text again.
var ErrBannedPattern = errors.New("comment rejected: matched a banned pattern")

// CompileBanned compiles every denylist entry as a regex and returns the first
// that does not parse. Banned words are regexes, not literals, because the
// denylist has to express shapes (variants of a brand name, leetspeak, URL
// fragments) a literal list cannot; a literal word is still a valid regex, so
// the common case costs nothing. Failing here — at Validate time — is the
// "validasi inline" contract: the editor learns about a broken pattern before
// it is ever saved, never when a comment is about to ship.
func CompileBanned(banned []string) ([]*regexp.Regexp, error) {
	if len(banned) == 0 {
		return nil, nil
	}
	patterns := make([]*regexp.Regexp, 0, len(banned))
	for _, raw := range banned {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		// (?i) rather than lowercasing the hay: the pattern must be matched
		// case-insensitively without the caller having to remember to lowercase
		// it, and lowercasing only one side would silently break an uppercase
		// pattern.
		re, err := regexp.Compile("(?i)" + raw)
		if err != nil {
			return nil, fmt.Errorf("%w: banned pattern %q does not compile: %v", ErrValidation, raw, err)
		}
		patterns = append(patterns, re)
	}
	return patterns, nil
}

// MatchBanned reports whether text hits any compiled denylist pattern, and
// returns the offending source pattern so the failure message names the rule
// that fired (an operator staring at "rejected" with no rule cannot fix the
// denylist). Case-insensitive: the denylist is authored once, and a poster's
// capitalisation must not be the reason a banned comment ships.
func MatchBanned(text string, patterns []*regexp.Regexp) (string, bool) {
	if len(patterns) == 0 {
		return "", false
	}
	for _, re := range patterns {
		if re.MatchString(text) {
			return re.String(), true
		}
	}
	return "", false
}
