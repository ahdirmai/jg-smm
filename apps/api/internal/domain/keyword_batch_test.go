package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// KeywordBatch crosses the API boundary to the web dashboard, whose types are
// camelCase. Without json tags encoding/json emits the Go field names
// ("PostsCount"), the compile stays green, and every field reads as `undefined`
// in the UI — which is how the batch dashboard shipped blank. These tags are
// the contract; this test is the only thing that fails when one is dropped.
func TestKeywordBatchJSONFieldNames(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	runID := "run-1"
	msg := "boom"
	user := "user-1"

	raw, err := json.Marshal(KeywordBatch{
		ID:            "b1",
		Platform:      PlatformInstagram,
		Keywords:      []string{"batulicin"},
		WindowFrom:    &from,
		WindowTo:      &to,
		MaxPosts:      50,
		ActorID:       "apify/instagram-scraper",
		Status:        KeywordBatchSucceeded,
		ApifyRunID:    &runID,
		ItemsRead:     12,
		PostsCount:    12,
		CommentsCount: 19,
		Error:         &msg,
		CreatedBy:     &user,
		CreatedAt:     created,
		FinishedAt:    &created,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := []string{
		"id", "platform", "keywords", "windowFrom", "windowTo", "maxPosts",
		"actorId", "status", "apifyRunId", "itemsRead", "postsCount",
		"commentsCount", "error", "createdBy", "createdAt", "finishedAt",
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Errorf("missing JSON key %q; the web type reads it as undefined", key)
		}
	}
	if len(got) != len(want) {
		t.Errorf("field count: got %d keys %v, want %d", len(got), keysOf(got), len(want))
	}

	// The values a dashboard renders must survive the round trip.
	if got["status"] != "SUCCEEDED" {
		t.Errorf("status = %v, want the raw status string", got["status"])
	}
	if got["postsCount"] != float64(12) {
		t.Errorf("postsCount = %v, want 12", got["postsCount"])
	}
	if !strings.HasPrefix(got["createdAt"].(string), "2026-09-25T10:00:00") {
		t.Errorf("createdAt = %v, want RFC3339", got["createdAt"])
	}

	// A nil window must serialize as null, not as year zero.
	var bare map[string]any
	b, _ := json.Marshal(KeywordBatch{ID: "b2"})
	if err := json.Unmarshal(b, &bare); err != nil {
		t.Fatalf("unmarshal bare: %v", err)
	}
	for _, key := range []string{"windowFrom", "windowTo", "error", "finishedAt"} {
		if v, ok := bare[key]; !ok || v != nil {
			t.Errorf("zero-value %s = %v, want null", key, v)
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestKeywordBatchStatusTerminal(t *testing.T) {
	terminal := map[KeywordBatchStatus]bool{
		KeywordBatchPending:   false,
		KeywordBatchRunning:   false,
		KeywordBatchSucceeded: true,
		KeywordBatchFailed:    true,
	}
	for s, want := range terminal {
		if got := s.IsTerminal(); got != want {
			t.Errorf("%s.IsTerminal() = %v, want %v", s, got, want)
		}
		if !s.Valid() {
			t.Errorf("%s.Valid() = false", s)
		}
	}
	if KeywordBatchStatus("BOGUS").Valid() {
		t.Error(`"BOGUS".Valid() = true`)
	}
}
