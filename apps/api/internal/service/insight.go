package service

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// PostInsight is a scraped post scored for ranking.
//
// Why a score and not just "sort by likes": a raw like count ranks a 3-day-old
// post above a 3-hour-old one, and ranks a 200k-follower account above a 2k one
// for reasons that have nothing to do with the content. The score combines the
// three things an operator actually picks by — how much engagement the post got,
// how efficiently (per follower), and how fresh it is — so the top of the list
// is "worth acting on now", not "biggest account".
type PostInsight struct {
	PostID     string `json:"postId"`
	ExternalID string `json:"externalId"`
	Platform   string `json:"platform"`
	Author     string `json:"author"`
	Permalink  string `json:"permalink"`
	// Caption is the first line, for the ranking row.
	Caption string `json:"caption"`
	// MediaURL is the thumbnail, when the post has one.
	MediaURL string `json:"mediaUrl"`

	Likes    int64 `json:"likes"`
	Comments int64 `json:"comments"`
	Shares   int64 `json:"shares"`
	// Engagement is likes + comments + shares, the platform-neutral total.
	Engagement int64 `json:"engagement"`
	// EngagementPerFollower is engagement divided by the author's follower
	// count, as a percentage. Nil when the follower count is unknown — a rate
	// against a missing denominator is not a small number, it is no number.
	EngagementPerFollower *float64 `json:"engagementPerFollowerPct"`

	AgeHours float64 `json:"ageHours"`
	// Score is the ranking key, higher first. Exposed so the UI can show WHY a
	// post is ranked where it is rather than presenting an opaque order.
	Score float64 `json:"score"`
	// Rank is 1-based position within the returned list.
	Rank int `json:"rank"`
}

// InsightWeights tunes the ranking. Zero values fall back to the documented
// defaults, so a caller can override one knob without restating the rest.
type InsightWeights struct {
	// Engagement is the base term's weight.
	Engagement float64
	// PerFollower rewards engagement efficiency; it is what lets a small
	// account outrank a large one that got the same absolute numbers.
	PerFollower float64
	// Freshness decays the score with age (half-life, in hours).
	Freshness          float64
	ReceyHalfLifeHours float64
}

// DefaultInsightWeights are the shipped ranking weights. Engagement dominates,
// per-follower is a strong secondary signal, and freshness decays over ~3 days
// (a post older than that is history, not an opportunity).
func DefaultInsightWeights() InsightWeights {
	return InsightWeights{
		Engagement:         1.0,
		PerFollower:        8.0,
		Freshness:          0.5,
		ReceyHalfLifeHours: 72,
	}
}

func (w InsightWeights) withDefaults() InsightWeights {
	d := DefaultInsightWeights()
	if w.Engagement == 0 {
		w.Engagement = d.Engagement
	}
	if w.PerFollower == 0 {
		w.PerFollower = d.PerFollower
	}
	if w.Freshness == 0 {
		w.Freshness = d.Freshness
	}
	if w.ReceyHalfLifeHours <= 0 {
		w.ReceyHalfLifeHours = d.ReceyHalfLifeHours
	}
	return w
}

// ScoreInput is one post's raw numbers, platform-neutral.
type ScoreInput struct {
	Likes     int64
	Comments  int64
	Shares    int64
	Followers *int64
	AgeHours  float64
}

// scorePost computes the ranking score for one post.
//
// The engagement term is log-scaled. A linear term lets one viral outlier
// (50k likes) flatten everything else to noise, so the list becomes one post and
// a wall of ties; log keeps the ordering while leaving the rest of the list
// legible. The +1 inside the log keeps a zero-engagement post at exactly 0
// instead of -Inf.
//
// Per-follower is a multiplier, not an additive term, so an efficient post with
// no absolute reach cannot leapfrog a genuinely popular one — it lifts a post
// relative to its own baseline.
//
// Freshness halves every ReceyHalfLifeHours. It is bounded to (0,1], so it can
// only ever discount, never inflate.
func scorePost(in ScoreInput, w InsightWeights) float64 {
	w = w.withDefaults()

	engagement := in.Likes + in.Comments + in.Shares
	base := math.Log1p(float64(engagement)) // 0 at zero engagement, sub-linear growth

	efficiency := 1.0
	if in.Followers != nil && *in.Followers > 0 {
		// Engagement as a fraction of followers, ×100 so the number is a
		// readable percentage rather than 0.0042.
		pct := float64(engagement) / float64(*in.Followers) * 100
		efficiency = 1 + (w.PerFollower * pct / 100)
	}

	freshness := 1.0
	if in.AgeHours > 0 {
		freshness = math.Pow(0.5, in.AgeHours/w.ReceyHalfLifeHours)
	}

	return w.Engagement * base * efficiency * (1 + w.Freshness*freshness)
}

// RankInsights scores and orders posts, highest first, assigning 1-based ranks.
//
// Ties break by engagement then by id, so the order is stable across calls: a
// list that reshuffles between two identical requests reads as a bug and makes
// "the top post" unclickable.
func RankInsights(posts []domain.Post, followers map[string]int64, weights InsightWeights) []PostInsight {
	out := make([]PostInsight, 0, len(posts))
	for _, p := range posts {
		likes, comments, shares := postCounts(p)
		var f *int64
		if v, ok := followers[p.AuthorHandle]; ok {
			f = &v
		}

		in := ScoreInput{Likes: likes, Comments: comments, Shares: shares, Followers: f, AgeHours: ageHours(p)}

		insight := PostInsight{
			PostID:     p.ID,
			ExternalID: p.ExternalID,
			Platform:   string(p.Platform),
			Author:     p.AuthorHandle,
			Permalink:  postPermalinkOf(p),
			Caption:    firstLine(p),
			MediaURL:   firstMediaURL(p),
			Likes:      likes,
			Comments:   comments,
			Shares:     shares,
			Engagement: likes + comments + shares,
			AgeHours:   math.Round(in.AgeHours*10) / 10,
			Score:      math.Round(scorePost(in, weights)*1000) / 1000,
		}
		if f != nil && *f > 0 {
			pct := float64(insight.Engagement) / float64(*f) * 100
			insight.EngagementPerFollower = &pct
		}
		out = append(out, insight)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Engagement != out[j].Engagement {
			return out[i].Engagement > out[j].Engagement
		}
		return out[i].PostID < out[j].PostID
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// postCounts reads likes/comments/shares out of the post's metrics blob.
//
// The keys are the platform-neutral names the ingestor normalises to
// (`comments` holds Threads' reply count, `shares` its repost count), so this
// needs no per-platform branch. A missing metric counts as 0 for RANKING only —
// it is a comparison key, and the UI shows the raw numbers separately, so an
// absent value never becomes an asserted "0 likes" in the interface.
func postCounts(p domain.Post) (likes, comments, shares int64) {
	if len(p.Metrics) == 0 {
		return 0, 0, 0
	}
	var m map[string]any
	if err := json.Unmarshal(p.Metrics, &m); err != nil {
		return 0, 0, 0
	}
	return metricInt(m, "likes"), metricInt(m, "comments"), metricInt(m, "shares")
}

// metricInt reads one metric as an int64, accepting the shapes the actors emit
// (JSON numbers arrive as float64, but a string sneaks in on some builds).
// Missing, non-numeric, or IMPOSSIBLE yields 0 without an error: the caller is
// ranking, and an unreadable metric is an absent one, not a failure.
//
// Negative values are rejected because the actor emits them: `likes: -1` is
// observed on live rows (Instagram hides the count on some media). A negative
// like is not a small number — it is "not reported" — and letting it through
// ranked those posts BELOW every zero-engagement post and printed "Likes -1".
func metricInt(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	var n int64
	switch t := v.(type) {
	case float64:
		n = int64(t)
	case int64:
		n = t
	case int:
		n = int64(t)
	case json.Number:
		if i, err := t.Int64(); err == nil {
			n = i
		} else {
			return 0
		}
	default:
		return 0
	}
	if n < 0 {
		return 0
	}
	return n
}

func ageHours(p domain.Post) float64 {
	// PublishedAt is when the post went live; ScrapedAt is when we looked. Age
	// must use the publish time — ranking by scrape time would rank every post
	// in one batch as equally fresh.
	ref := p.ScrapedAt
	if p.PublishedAt != nil {
		ref = *p.PublishedAt
	}
	h := time.Since(ref).Hours()
	if h < 0 || math.IsNaN(h) {
		return 0
	}
	return h
}

func firstLine(p domain.Post) string {
	if p.Text == nil {
		return ""
	}
	s := *p.Text
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	if len(s) > 140 {
		return s[:140]
	}
	return s
}

func firstMediaURL(p domain.Post) string {
	if len(p.MediaURLs) > 0 {
		return p.MediaURLs[0]
	}
	return ""
}

// postPermalinkOf returns the post's own URL, or "" when the platform's shape is
// unknown. The web layer has the same helper; this one exists so the ranking row
// is self-contained on the wire.
func postPermalinkOf(p domain.Post) string {
	switch p.Platform {
	case domain.PlatformInstagram:
		return fmt.Sprintf("https://www.instagram.com/p/%s/", p.ExternalID)
	case domain.PlatformThreads:
		if p.AuthorHandle != "" {
			return fmt.Sprintf("https://www.threads.net/@%s/post/%s", p.AuthorHandle, p.ExternalID)
		}
	}
	return ""
}
