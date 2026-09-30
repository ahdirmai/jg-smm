package service

import (
	"context"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// raw_payload.bytes is a NOT NULL column with a CHECK (bytes >= 0), so "0" is a
// legal value and therefore looked fine. It was, in fact, every value ever
// written: both producers built the row without a size while the runner had
// already computed one per key from storage.Put and threw it away. The audit
// table claimed every payload was zero bytes.
func TestRawPayloadBytesRecordsTheSizeTheRunnerReported(t *testing.T) {
	store := newFakeScrapeStore()
	// ingestingRunner returns a real IG post row; a zero-item run would now be
	// rejected by the M-9 guard before any payload row is written.
	runner, storage := ingestingRunner(t, "ZZZ1")
	key := "run/ZZZ1.json"
	body := storage.items[key]
	if len(body) == 0 {
		t.Fatal("fixture stored no body; the assertion below would be vacuous")
	}

	svc := NewScrapeService(store, runner, actorForAny, storage, nil)
	if _, err := svc.ScrapeTarget(context.Background(), domain.PlatformInstagram,
		"https://www.instagram.com/p/ZZZ1/", true); err != nil {
		t.Fatalf("ScrapeTarget: %v", err)
	}

	if got := store.payloadBytesOf(key); got != int64(len(body)) {
		t.Fatalf("raw_payload.bytes = %d, want the %d bytes storage.Put reported; "+
			"a 0 here means the audit table is lying about the run size", got, len(body))
	}
}

// The same has to hold on the keyword path — it is a different runner method
// with its own write loop, so a fix to one says nothing about the other.
func TestKeywordScrapeRecordsTheSizeTheRunnerReported(t *testing.T) {
	store := newFakeScrapeStore()
	storage := &fakeRawStorage{items: map[string][]byte{}}
	body := []byte(`{"id":"X1","shortCode":"abc","likesCount":1}`)
	key := "run/page-0.json"
	storage.items[key] = body

	runner := &fakeRunner{out: port.ApifyOutput{
		Status: "SUCCEEDED",
		RunID:  "r1",
		Items:  []port.RawItem{{Key: key, Bytes: int64(len(body))}},
	}}
	svc := NewKeywordScrapeService(store, runner,
		func(domain.Platform) string { return "apify/instagram-scraper" }, storage, nil)
	svc.SetBatchStore(newFakeKeywordBatchStore())
	if _, err := svc.ScrapeKeywords(context.Background(), KeywordScrapeInput{
		Platform: domain.PlatformInstagram,
		Keywords: []string{"nasa"},
		MaxPosts: 5,
	}); err != nil {
		t.Fatalf("ScrapeKeywords: %v", err)
	}

	if got := store.payloadBytesOf(key); got != int64(len(body)) {
		t.Fatalf("raw_payload.bytes on the keyword path = %d, want %d", got, len(body))
	}
}
