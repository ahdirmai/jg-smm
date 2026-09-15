// Package analytics is the 3rd-party provider adapter for official-account
// metrics (P2-11).
//
// The domain never knows which provider it is talking to: this adapter is the
// only place that speaks the provider's HTTP shape. P2 ships a deterministic
// stub provider so the ingestor, the API and the dashboard can be built and
// verified end to end before the real contract is signed. Swapping the stub for
// the real adapter touches this package only.
package analytics

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// StubProvider is a deterministic stand-in for the real 3rd-party analytics
// provider. It returns plausible-looking metrics derived from the account
// handle so the dashboard and the ingestor can be exercised without a contract.
//
// ponytail: replace with the real adapter once the provider contract is signed.
// The ingestor and the API must not change — only this package does.
type StubProvider struct {
	baseURL string
	key     string
	healthy bool
}

// New validates config. A missing key is an error even for the stub so a
// misconfigured production deploy fails loudly at boot rather than silently
// returning zeros.
func New(baseURL, key string) (*StubProvider, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("analytics: base url is required")
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("analytics: provider key is required")
	}
	return &StubProvider{baseURL: strings.TrimRight(baseURL, "/"), key: key, healthy: true}, nil
}

var _ port.AnalyticsProvider = (*StubProvider)(nil)

// Health reports whether the provider is reachable. The stub is always healthy
// once configured; the ingestor uses this to classify a partial run.
func (p *StubProvider) Health(ctx context.Context) error {
	if !p.healthy {
		return errors.New("analytics: provider unhealthy")
	}
	return nil
}

// FetchMetrics returns one snapshot per call. Values drift slowly from a seed
// derived from the handle so a dashboard shows a realistic trend, not a flat
// line — the point is exercising the chart, not fooling anyone.
func (p *StubProvider) FetchMetrics(ctx context.Context, acc domain.OfficialAccount) (domain.AnalyticsSnapshot, error) {
	if err := p.Health(ctx); err != nil {
		return domain.AnalyticsSnapshot{}, err
	}
	seed := handleSeed(acc.Handle)
	now := time.Now().UTC()
	f := float64(seed%9000 + 1000)
	day := float64(now.Unix() / 86400)
	drift := 1 + 0.02*float64(seed%7) + 0.001*day

	followers := int64(f * drift)
	return domain.AnalyticsSnapshot{
		OfficialAccountID: acc.ID,
		Platform:          acc.Platform,
		TS:                now,
		Followers:         &followers,
		Reach:             ptrInt64(int64(float64(followers) * 0.4 * (1 + 0.3*rand.Float64()))),
		Views:             ptrInt64(int64(float64(followers) * 0.25 * (1 + 0.5*rand.Float64()))),
		Engagements:       ptrInt64(int64(float64(followers) * 0.06)),
		ProfileViews:      ptrInt64(int64(float64(followers) * 0.02)),
		Metrics:           []byte(fmt.Sprintf(`{"provider":"stub","fetched":"%s"}`, now.Format(time.RFC3339))),
		Provider:          domain.AnalyticsProviderA,
		FetchedAt:         now,
	}, nil
}

// FetchMentions returns a small deterministic set of mentions. The stub never
// fabricates a spike: alerting (P2-07) is tested with real seeds, not luck.
func (p *StubProvider) FetchMentions(ctx context.Context, acc domain.OfficialAccount) ([]domain.AnalyticsMention, error) {
	if err := p.Health(ctx); err != nil {
		return nil, err
	}
	if acc.LastFetchedAt != nil && time.Since(*acc.LastFetchedAt) < time.Minute {
		// Nothing new since the last pull — the same behaviour the real adapter
		// will have when the provider has no delta.
		return nil, nil
	}
	now := time.Now().UTC()
	seed := handleSeed(acc.Handle)
	n := int(1 + seed%3)
	out := make([]domain.AnalyticsMention, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, domain.AnalyticsMention{
			OfficialAccountID: acc.ID,
			Platform:          acc.Platform,
			ExternalID:        fmt.Sprintf("%s-mention-%d-%d", acc.Handle, now.Unix(), i),
			AuthorHandle:      ptrString(fmt.Sprintf("user%d", (seed+int64(i))%200)),
			Text:              fmt.Sprintf("Mention %d of @%s", i, acc.Handle),
			URL:               fmt.Sprintf("https://%s/post/%d", acc.Platform, (seed+int64(i))%9999),
			PostedAt:          now.Add(-time.Duration(i+1) * time.Hour),
			Sentiment:         ptrString([]string{"positive", "neutral", "negative"}[(seed+int64(i))%3]),
			FetchedAt:         now,
		})
	}
	return out, nil
}

// handleSeed turns a handle into a stable number so the same account always
// charts the same shape between calls (a dashboard that repaints a different
// curve on every refresh would look broken, even with fake data).
func handleSeed(handle string) int64 {
	var h int64
	for _, c := range handle {
		h = h*31 + int64(c)
	}
	if h < 0 {
		h = -h
	}
	return h
}

func ptrInt64(n int64) *int64    { return &n }
func ptrString(s string) *string { return &s }
