package service

import (
	"context"
	"errors"
	"math/rand"
	"testing"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// P3-02 template engine unit tests. The store is a fake (the dedupe query is
// proven against real Postgres in template_test.go); what is left to prove
// here is the engine: weighted distribution, placeholder rejection, and the
// empty-pool sentinel.

// fakeTemplateStore is a scripted port.TemplateStore. PickForTarget returns the
// candidates the test set, so the engine is exercised independently of SQL.
type fakeTemplateStore struct {
	candidates []domain.CommentTemplate
	created    []domain.CommentTemplate
	updated    []domain.CommentTemplate
	deleted    []string
}

func (f *fakeTemplateStore) CreateTemplate(ctx context.Context, t domain.CommentTemplate) (domain.CommentTemplate, error) {
	f.created = append(f.created, t)
	return t, nil
}
func (f *fakeTemplateStore) GetTemplate(ctx context.Context, id string) (domain.CommentTemplate, error) {
	return domain.CommentTemplate{}, domain.ErrNotFound
}
func (f *fakeTemplateStore) ListTemplates(ctx context.Context, p domain.Platform, includeInactive bool, limit, offset *int) ([]domain.CommentTemplate, error) {
	return f.candidates, nil
}
func (f *fakeTemplateStore) UpdateTemplate(ctx context.Context, t domain.CommentTemplate) (domain.CommentTemplate, error) {
	f.updated = append(f.updated, t)
	return t, nil
}
func (f *fakeTemplateStore) DeleteTemplate(ctx context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeTemplateStore) PickForTarget(ctx context.Context, p domain.Platform, targetID string) ([]domain.CommentTemplate, error) {
	return f.candidates, nil
}

func newTestTemplateService(store port.TemplateStore, rng *rand.Rand) *TemplateService {
	return &TemplateService{templates: store, clock: systemClock{}, rng: rng, logger: nil}
}

// TestTemplateWeightedPick asserts weight actually biases the draw: a weight-3
// variant is picked ~3x as often as a weight-1 one over many draws.
func TestTemplateWeightedPick(t *testing.T) {
	store := &fakeTemplateStore{
		candidates: []domain.CommentTemplate{
			{ID: "heavy", Text: "heavy {topic}", Vars: []string{"topic"}, Weight: 3},
			{ID: "light", Text: "light {topic}", Vars: []string{"topic"}, Weight: 1},
		},
	}
	svc := newTestTemplateService(store, rand.New(rand.NewSource(42)))

	counts := map[string]int{}
	values := map[string]string{"topic": "launch"}
	for i := 0; i < 4000; i++ {
		got, err := svc.Pick(context.Background(), domain.PlatformInstagram, "target-x", values)
		if err != nil {
			t.Fatalf("pick %d: %v", i, err)
		}
		if got.RenderedText != "heavy launch" && got.RenderedText != "light launch" {
			t.Fatalf("unexpected render %q", got.RenderedText)
		}
		counts[got.TemplateID]++
	}
	// 3:1 over 4000 draws -> expect heavy in ~3000 (+/- noise). A loose band
	// keeps the test stable while still catching a swapped or ignored weight.
	if counts["heavy"] < 2700 || counts["heavy"] > 3300 {
		t.Fatalf("weight not respected: heavy=%d light=%d", counts["heavy"], counts["light"])
	}
}

// TestTemplatePickSingleCandidate is the common case: one good variant, so the
// pool is a certainty and the engine must not overthink it.
func TestTemplatePickSingleCandidate(t *testing.T) {
	store := &fakeTemplateStore{
		candidates: []domain.CommentTemplate{
			{ID: "only", Text: "nice {topic} and {product}", Vars: []string{"topic", "product"}, Weight: 1},
		},
	}
	svc := newTestTemplateService(store, rand.New(rand.NewSource(1)))

	got, err := svc.Pick(context.Background(), domain.PlatformInstagram, "target-y", map[string]string{
		"topic":   "launch",
		"product": "sneakers",
	})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if got.TemplateID != "only" || got.RenderedText != "nice launch and sneakers" {
		t.Fatalf("expected the only candidate rendered, got %+v", got)
	}
}

// TestTemplatePickUnresolvedPlaceholder asserts a worker never receives a
// half-rendered comment: a var without a value leaves {topic} in the text and
// the pick must fail instead of posting it.
func TestTemplatePickUnresolvedPlaceholder(t *testing.T) {
	store := &fakeTemplateStore{
		candidates: []domain.CommentTemplate{
			{ID: "t1", Text: "love the {topic}", Vars: []string{"topic"}, Weight: 1},
		},
	}
	svc := newTestTemplateService(store, rand.New(rand.NewSource(1)))

	if _, err := svc.Pick(context.Background(), domain.PlatformInstagram, "target-z", map[string]string{}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for an unresolved placeholder, got %v", err)
	}
}

// TestTemplatePickEmptyPool asserts the honest outcome when every active variant
// is already used against this target inside 7 days: skip the comment, do not
// repeat one.
func TestTemplatePickEmptyPool(t *testing.T) {
	store := &fakeTemplateStore{candidates: nil}
	svc := newTestTemplateService(store, rand.New(rand.NewSource(1)))

	if _, err := svc.Pick(context.Background(), domain.PlatformInstagram, "target-w", nil); !errors.Is(err, domain.ErrTemplatePoolEmpty) {
		t.Fatalf("expected ErrTemplatePoolEmpty, got %v", err)
	}
}

// TestTemplateValidate covers the cross-field invariants the schema cannot
// express: declared vars must be used and referenced vars must be declared.
func TestTemplateValidate(t *testing.T) {
	cases := []struct {
		name    string
		tmpl    domain.CommentTemplate
		wantErr bool
	}{
		{"ok", domain.CommentTemplate{Text: "love {topic}", Vars: []string{"topic"}, Weight: 1}, false},
		{"fixed text no vars", domain.CommentTemplate{Text: "great post", Weight: 1}, false},
		{"blank text", domain.CommentTemplate{Text: "  ", Weight: 1}, true},
		{"zero weight", domain.CommentTemplate{Text: "x", Weight: 0}, true},
		{"undeclared var in text", domain.CommentTemplate{Text: "love {topic}", Vars: []string{"product"}, Weight: 1}, true},
		{"declared var unused", domain.CommentTemplate{Text: "love this", Vars: []string{"topic"}, Weight: 1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.tmpl.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tc.wantErr && !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}

// TestScreenBanned covers the denylist the enqueue path screens a rendered
// comment against (P3-03 wraps this into the pipeline).
func TestScreenBanned(t *testing.T) {
	if !ScreenBanned("this is SPAM", []string{"spam"}) {
		t.Fatal("case-insensitive match expected")
	}
	if !ScreenBanned("buy now!!!", []string{"buy now"}) {
		t.Fatal("phrase match expected")
	}
	if ScreenBanned("clean text", nil) {
		t.Fatal("nil denylist must not flag clean text")
	}
	if ScreenBanned("clean text", []string{""}) {
		t.Fatal("empty word must not flag everything")
	}
}
