package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

func insightPost(id string, likes, comments, shares int64, ageHours float64) domain.Post {
	m, _ := json.Marshal(map[string]any{"likes": likes, "comments": comments, "shares": shares})
	pub := time.Now().Add(-time.Duration(ageHours) * time.Hour)
	txt := "caption for " + id
	return domain.Post{
		ID: id, ExternalID: id, Platform: domain.PlatformInstagram,
		AuthorHandle: "author", Text: &txt, Metrics: m, PublishedAt: &pub,
		MediaURLs: []string{"https://cdn.example/img.jpg"},
	}
}

// Engagement must dominate: a post with real engagement outranks one with none,
// whatever the age. If this inverts, the page becomes a "newest first" list with
// extra steps.
func TestRankInsightsEngagementBeatsAge(t *testing.T) {
	old := insightPost("old", 500, 40, 10, 100) // 100h old, lots of engagement
	fresh := insightPost("fresh", 1, 0, 0, 1)   // 1h old, nothing
	got := RankInsights([]domain.Post{old, fresh}, nil, InsightWeights{})

	if got[0].PostID != "old" {
		t.Fatalf("top = %q, want the engaging post (old) — engagement must dominate freshness", got[0].PostID)
	}
	if got[0].Rank != 1 || got[1].Rank != 2 {
		t.Fatalf("ranks = %d,%d, want 1,2", got[0].Rank, got[1].Rank)
	}
}

// With engagement equal, the fresher post wins. This is the freshness half of
// the score, and without it every post of a batch ties.
func TestRankInsightsFreshnessBreaksEngagementTies(t *testing.T) {
	fresh := insightPost("fresh", 50, 5, 0, 2)
	stale := insightPost("stale", 50, 5, 0, 60) // same engagement, 30x older
	got := RankInsights([]domain.Post{fresh, stale}, nil, InsightWeights{})

	if got[0].PostID != "fresh" {
		t.Fatalf("top = %q, want the fresher of two equal posts", got[0].PostID)
	}
}

// A small account that performed well relative to its size must be able to
// outrank a large one with the same absolute numbers. That is the whole point of
// the per-follower term — otherwise the ranking is a follower-count proxy.
func TestRankInsightsPerFollowerLiftsSmallAccount(t *testing.T) {
	small := insightPost("small", 100, 10, 0, 5)
	big := insightPost("big", 100, 10, 0, 5) // identical engagement + age
	small.AuthorHandle = "small_acct"
	big.AuthorHandle = "big_acct"

	followers := map[string]int64{"small_acct": 1_000, "big_acct": 1_000_000}
	got := RankInsights([]domain.Post{big, small}, followers, InsightWeights{})

	if got[0].PostID != "small" {
		t.Fatalf("top = %q, want small_acct — same engagement, 1000x better rate", got[0].PostID)
	}
	if got[0].EngagementPerFollower == nil || got[1].EngagementPerFollower == nil {
		t.Fatal("both rates must be reported when both denominators are known")
	}
	if *got[0].EngagementPerFollower <= *got[1].EngagementPerFollower {
		t.Fatalf("small rate %v must exceed big rate %v", *got[0].EngagementPerFollower, *got[1].EngagementPerFollower)
	}
}

// No follower count → no rate. A rate against an unknown denominator is not 0%,
// and printing one would rank an unmeasured post last for a reason that is not
// about its performance.
func TestRankInsightsOmitsRateWithoutFollowers(t *testing.T) {
	got := RankInsights([]domain.Post{insightPost("p", 10, 1, 0, 1)}, nil, InsightWeights{})
	if got[0].EngagementPerFollower != nil {
		t.Fatalf("rate = %v, want nil (no denominator known)", *got[0].EngagementPerFollower)
	}
}

// Ties must be stable. A list that reshuffles between two identical requests
// makes "the top post" unclickable.
func TestRankInsightsIsDeterministic(t *testing.T) {
	posts := []domain.Post{
		insightPost("b", 10, 1, 0, 5),
		insightPost("a", 10, 1, 0, 5),
		insightPost("c", 10, 1, 0, 5),
	}
	first := RankInsights(posts, nil, InsightWeights{})
	for range 5 {
		again := RankInsights(posts, nil, InsightWeights{})
		for j := range first {
			if first[j].PostID != again[j].PostID {
				t.Fatalf("order changed between calls: %q vs %q at %d", first[j].PostID, again[j].PostID, j)
			}
		}
	}
}

// A post with no metrics at all must score 0 and not panic: keyword batches can
// store a post before any metrics arrive.
func TestRankInsightsHandlesMissingMetrics(t *testing.T) {
	p := domain.Post{ID: "empty", ExternalID: "empty", Platform: domain.PlatformInstagram, AuthorHandle: "x"}
	got := RankInsights([]domain.Post{p}, nil, InsightWeights{})
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Score != 0 {
		t.Fatalf("score = %v, want 0 for a post with no metrics", got[0].Score)
	}
	if got[0].Engagement != 0 {
		t.Fatalf("engagement = %d, want 0", got[0].Engagement)
	}
}

// The score must be monotonic in engagement: more engagement, never a lower
// score, at the same age. A log term that went negative would invert the list.
func TestRankInsightsScoreIsMonotonicInEngagement(t *testing.T) {
	var prev float64 = -1
	for _, n := range []int64{0, 1, 10, 100, 1000, 10000} {
		p := insightPost("p", n, 0, 0, 5)
		got := RankInsights([]domain.Post{p}, nil, InsightWeights{})
		if got[0].Score < prev {
			t.Fatalf("engagement %d scored %v, lower than the previous %v", n, got[0].Score, prev)
		}
		prev = got[0].Score
	}
}

// A future publish time (clock skew between us and the platform) must not score
// as infinitely fresh: age clamps at 0.
func TestRankInsightsClampsNegativeAge(t *testing.T) {
	p := insightPost("future", 10, 0, 0, -5) // published 5h in the future
	got := RankInsights([]domain.Post{p}, nil, InsightWeights{})
	if got[0].AgeHours != 0 {
		t.Fatalf("age = %v, want 0 (clamped) for a future timestamp", got[0].AgeHours)
	}
	if got[0].Score <= 0 {
		t.Fatalf("score = %v, want positive", got[0].Score)
	}
}

// The ranking row must carry what the UI renders: a thumbnail, a caption line,
// and a permalink to open the post.
func TestRankInsightsCarriesDisplayFields(t *testing.T) {
	p := insightPost("p1", 10, 2, 0, 3)
	got := RankInsights([]domain.Post{p}, nil, InsightWeights{})
	if got[0].Permalink == "" {
		t.Error("permalink must be set, or the row cannot open the post")
	}
	if got[0].MediaURL == "" {
		t.Error("mediaUrl must be carried for the thumbnail")
	}
	if got[0].Caption == "" {
		t.Error("caption must be carried")
	}
	if got[0].ExternalID != "p1" {
		t.Errorf("externalId = %q", got[0].ExternalID)
	}
}

// A multi-line caption must be truncated to its first line: the row is one line
// tall, and a full caption would push the ranking apart.
func TestRankInsightsCaptionIsFirstLineOnly(t *testing.T) {
	txt := "first line\nsecond line\nthird"
	p := domain.Post{ID: "x", ExternalID: "x", Platform: domain.PlatformInstagram, Text: &txt}
	got := RankInsights([]domain.Post{p}, nil, InsightWeights{})
	if got[0].Caption != "first line" {
		t.Fatalf("caption = %q, want only the first line", got[0].Caption)
	}
}

// Weights must be overridable one at a time; a caller that sets only Freshness
// must not silently zero the engagement term.
func TestInsightWeightsPartialOverrideKeepsDefaults(t *testing.T) {
	w := InsightWeights{Freshness: 0.1}.withDefaults()
	d := DefaultInsightWeights()
	if w.Engagement != d.Engagement {
		t.Errorf("engagement = %v, want the default %v", w.Engagement, d.Engagement)
	}
	if w.PerFollower != d.PerFollower {
		t.Errorf("perFollower = %v, want the default %v", w.PerFollower, d.PerFollower)
	}
	if w.Freshness != 0.1 {
		t.Errorf("freshness = %v, want the caller's 0.1", w.Freshness)
	}
}

// A negative metric is "not reported", not a small number. Instagram hides the
// like count on some media and the actor emits -1; letting that through ranked
// those posts BELOW every zero-engagement post and the UI printed "Likes -1".
func TestMetricIntRejectsNegative(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want int64
	}{
		{"negative is treated as absent", float64(-1), 0},
		{"very negative", float64(-999), 0},
		{"zero stays zero", float64(0), 0},
		{"positive passes", float64(42), 42},
		{"string-like is absent", "12", 0},
		{"nil is absent", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := metricInt(map[string]any{"likes": tc.in}, "likes")
			if got != tc.want {
				t.Errorf("metricInt(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// The score must not go negative on a post whose only metric is a hidden count.
func TestRankInsightsNegativeMetricsDoNotGoNegative(t *testing.T) {
	m := json.RawMessage(`{"likes":-1,"comments":5}`)
	p := domain.Post{ID: "p", ExternalID: "p", Platform: domain.PlatformInstagram, Metrics: m}
	got := RankInsights([]domain.Post{p}, nil, InsightWeights{})
	if got[0].Likes < 0 {
		t.Errorf("likes = %d, want >= 0 (a hidden count is not a negative like)", got[0].Likes)
	}
	if got[0].Score < 0 {
		t.Errorf("score = %v, want >= 0", got[0].Score)
	}
	if got[0].Engagement != 5 {
		t.Errorf("engagement = %d, want 5 (the 5 comments, not -1 likes)", got[0].Engagement)
	}
}
