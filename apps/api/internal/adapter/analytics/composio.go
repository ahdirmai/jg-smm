package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// Composio pulls official-account metrics from Composio, which proxies each
// platform's own Graph API behind one tool-execute endpoint. The official
// account's ProviderRef holds the Composio connected-account id ("ca_..."); the
// handle is decorative because Composio keys on the connection, not the handle.
//
// Only Instagram is wired. The other platforms in the dashboard have no
// adapter here yet, so their accounts fail loudly at ingest rather than
// writing zeros.
//
// ponytail: TikTok has no insights tool in Composio at all (only
// GET_USER_STATS: followers/likes/video_count), so TikTok reach cannot be
// sourced from this connector. Add a toolkit adapter when one exists.
type Composio struct {
	baseURL string
	apiKey  string
	hc      *http.Client

	// entityCache maps connected-account id -> Composio user id. The tool
	// endpoint rejects a call that names a connection without its owner, and
	// the owner never changes for a connection's lifetime, so one lookup per
	// process is enough.
	entityCache sync.Map
}

// New builds a Composio-backed provider.
func NewComposio(baseURL, apiKey string) (*Composio, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("analytics: composio base url is required")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("analytics: composio api key is required")
	}
	return &Composio{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		hc:      &http.Client{Timeout: 30 * time.Second},
	}, nil
}

var _ port.AnalyticsProvider = (*Composio)(nil)

// Health reports whether the key authenticates. Listing connections is the
// cheapest call that proves the credential without touching a real account.
func (c *Composio) Health(ctx context.Context) error {
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := c.get(ctx, "/connected_accounts?limit=1", &page); err != nil {
		return fmt.Errorf("composio health: %w", err)
	}
	return nil
}

// FetchMetrics reads account-level Instagram insights.
//
// The snapshot ts is truncated to the UTC day on purpose. Instagram's
// `total_value` for period=day is a running total for the *current* day, not
// an increment, so storing one row per fetch and summing them per day would
// multiply a single day by the number of ingests. Day-truncated ts makes the
// (official_account_id, ts, provider) upsert collapse each day's ingests into
// one row that keeps refreshing — which is what the daily trend query sums.
func (c *Composio) FetchMetrics(ctx context.Context, acc domain.OfficialAccount) (domain.AnalyticsSnapshot, error) {
	if acc.Platform != domain.PlatformInstagram {
		return domain.AnalyticsSnapshot{}, fmt.Errorf("composio: no adapter for platform %q", acc.Platform)
	}
	var caID string
	if acc.ProviderRef != nil {
		caID = strings.TrimSpace(*acc.ProviderRef)
	}
	if caID == "" {
		return domain.AnalyticsSnapshot{}, fmt.Errorf("composio: account %q has no connected account id (provider_ref is empty)", acc.Handle)
	}
	entity, err := c.entityID(ctx, caID)
	if err != nil {
		return domain.AnalyticsSnapshot{}, err
	}

	// One call for every account-level metric. Instagram omits a metric it has
	// no data for rather than erroring, so a partial answer is normal: the
	// missing ones stay nil and the dashboard renders "—", never a fake 0.
	var insights struct {
		Data []struct {
			Name       string `json:"name"`
			TotalValue struct {
				Value *int64 `json:"value"`
			} `json:"total_value"`
		} `json:"data"`
	}
	if err := c.execute(ctx, "INSTAGRAM_GET_USER_INSIGHTS", caID, entity, map[string]any{
		"metric":      []string{"reach", "views", "profile_views", "accounts_engaged", "total_interactions", "likes", "comments", "saves", "shares"},
		"metric_type": "total_value",
		"period":      "day",
	}, &insights); err != nil {
		return domain.AnalyticsSnapshot{}, err
	}

	values := make(map[string]int64, len(insights.Data))
	for _, row := range insights.Data {
		if row.TotalValue.Value != nil {
			values[row.Name] = *row.TotalValue.Value
		}
	}

	// Followers live on the user object, not insights: Instagram silently drops
	// follower_count from the insights response.
	var user struct {
		FollowersCount *int64 `json:"followers_count"`
	}
	if err := c.execute(ctx, "INSTAGRAM_GET_USER_INFO", caID, entity, map[string]any{}, &user); err != nil {
		return domain.AnalyticsSnapshot{}, err
	}
	if user.FollowersCount != nil {
		values["followers_count"] = *user.FollowersCount
	}

	now := time.Now().UTC()
	raw, err := json.Marshal(map[string]any{
		"source":            "composio",
		"connected_account": caID,
		"instagram":         values,
	})
	if err != nil {
		return domain.AnalyticsSnapshot{}, fmt.Errorf("composio: encode metrics: %w", err)
	}

	return domain.AnalyticsSnapshot{
		OfficialAccountID: acc.ID,
		Platform:          acc.Platform,
		TS:                now.Truncate(24 * time.Hour),
		Followers:         intPtr(values, "followers_count"),
		Reach:             intPtr(values, "reach"),
		Views:             intPtr(values, "views"),
		Engagements:       intPtr(values, "total_interactions"),
		ProfileViews:      intPtr(values, "profile_views"),
		// No mention source: the Instagram toolkit exposes no mentions tool.
		Mentions:  nil,
		Metrics:   raw,
		Provider:  domain.AnalyticsProviderA,
		FetchedAt: now,
	}, nil
}

// FetchMentions returns nothing. Instagram's toolkit publishes no mentions
// tool, so the mention alert rule has no source on this platform yet.
func (c *Composio) FetchMentions(context.Context, domain.OfficialAccount) ([]domain.AnalyticsMention, error) {
	return nil, nil
}

// contentMetricLimit caps the per-content fan-out. Fetching insights is one
// HTTP call per post, so this is the knob that decides how much a single
// account's ingest costs. Ten is the ceiling on the ingest path; the API's
// limit parameter only ever asks for less.
const contentMetricLimit = 10

// perContentMetrics is the metric set requested for every media. It is
// deliberately NOT the account-level set: `profile_visits` and `follows` are
// account-scoped on Instagram and asking a media for them fails the WHOLE call
// with "does not support ... for this media product type", taking the metrics
// that do work down with it. `impressions` is absent because Instagram
// deprecated it outright, and `replies` because a carousel rejects it. One set
// has to serve feed posts and reels alike, so it is the intersection.
var perContentMetrics = []string{
	"reach", "views", "total_interactions", "likes", "comments", "saved", "shares",
}

// FetchContentMetrics returns per-post performance for the account's most
// recent posts. Two calls plus one per post: the IG user id comes from the user
// object, the post list from the user-media endpoint, and each post's metrics
// are a separate call because Instagram has no batch media-insights endpoint.
//
// These are `lifetime` totals — a running count since publication, not a
// per-day increment — so the caller must upsert by media id rather than append
// a row per run.
func (c *Composio) FetchContentMetrics(ctx context.Context, acc domain.OfficialAccount, limit int) ([]domain.ContentMetric, error) {
	if acc.Platform != domain.PlatformInstagram {
		return nil, nil
	}
	caID, err := c.connectedAccount(acc)
	if err != nil {
		return nil, err
	}
	entity, err := c.entityID(ctx, caID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > contentMetricLimit {
		limit = contentMetricLimit
	}

	// The media endpoints are IG-scoped: they want the Instagram user id, which
	// is not the id INSTAGRAM_GET_USER_INFO reports under "id" for every account
	// type. Both resolve to the same profile, so take the one the user object
	// gives and let the call fail loudly if it is ever wrong.
	var user struct {
		ID string `json:"id"`
	}
	if err := c.execute(ctx, "INSTAGRAM_GET_USER_INFO", caID, entity, map[string]any{}, &user); err != nil {
		return nil, err
	}
	if user.ID == "" {
		return nil, fmt.Errorf("composio: account %q reports no instagram user id", acc.Handle)
	}

	var listing struct {
		Data []struct {
			ID                string  `json:"id"`
			Caption           *string `json:"caption"`
			MediaType         string  `json:"media_type"`
			MediaProductType  string  `json:"media_product_type"`
			Permalink         *string `json:"permalink"`
			Timestamp         string  `json:"timestamp"`
			LikeCount         *int64  `json:"like_count"`
			CommentsCount     *int64  `json:"comments_count"`
			InsightsPlayCount *int64  `json:"insights_play_count"`
		} `json:"data"`
	}
	if err := c.execute(ctx, "INSTAGRAM_GET_IG_USER_MEDIA", caID, entity, map[string]any{
		"ig_user_id": user.ID,
		"fields":     "id,caption,media_type,media_product_type,permalink,timestamp,like_count,comments_count",
		"limit":      limit,
	}, &listing); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	out := make([]domain.ContentMetric, 0, len(listing.Data))
	for _, m := range listing.Data {
		if m.ID == "" {
			continue
		}
		var insights struct {
			Data []struct {
				Name   string `json:"name"`
				Values []struct {
					Value *int64 `json:"value"`
				} `json:"values"`
			} `json:"data"`
		}
		if err := c.execute(ctx, "INSTAGRAM_GET_IG_MEDIA_INSIGHTS", caID, entity, map[string]any{
			"ig_media_id": m.ID,
			"metric":      strings.Join(perContentMetrics, ","),
		}, &insights); err != nil {
			// One unreadable post must not cost the other nine: the account's
			// per-content list is still worth writing. The post simply keeps
			// whatever it had, and the run's error count says the list is short.
			return out, fmt.Errorf("composio: media insights for %s: %w", m.ID, err)
		}

		values := make(map[string]int64, len(insights.Data))
		for _, row := range insights.Data {
			if len(row.Values) > 0 && row.Values[0].Value != nil {
				values[row.Name] = *row.Values[0].Value
			}
		}
		// like_count and comments_count ride on the post object rather than
		// insights, but they are the numbers a reader expects beside a caption,
		// so they belong in the raw blob with the rest.
		raw, err := json.Marshal(map[string]any{
			"source":         "composio",
			"media_id":       m.ID,
			"instagram":      values,
			"like_count":     m.LikeCount,
			"comments_count": m.CommentsCount,
		})
		if err != nil {
			return out, fmt.Errorf("composio: encode media metrics for %s: %w", m.ID, err)
		}

		cm := domain.ContentMetric{
			OfficialAccountID: acc.ID,
			Platform:          acc.Platform,
			ExternalID:        m.ID,
			ContentType:       strPtr(m.MediaType),
			Caption:           m.Caption,
			Permalink:         m.Permalink,
			PublishedAt:       parseIGTime(m.Timestamp),
			Reach:             intPtr(values, "reach"),
			Views:             intPtr(values, "views"),
			Interactions:      intPtr(values, "total_interactions"),
			Likes:             intPtr(values, "likes"),
			Comments:          intPtr(values, "comments"),
			Saves:             intPtr(values, "saved"),
			Shares:            intPtr(values, "shares"),
			Metrics:           raw,
			Provider:          domain.AnalyticsProviderA,
			FetchedAt:         now,
		}

		// media_product_type distinguishes a reel from a feed video; both report
		// media_type VIDEO but rank and read differently.
		if m.MediaProductType != "" {
			cm.ContentType = strPtr(m.MediaProductType)
		}
		out = append(out, cm)
	}
	return out, nil
}

// connectedAccount resolves the Composio connection an account is bound to.
func (c *Composio) connectedAccount(acc domain.OfficialAccount) (string, error) {
	if acc.ProviderRef == nil {
		return "", fmt.Errorf("composio: account %q has no connected account id (provider_ref is empty)", acc.Handle)
	}
	caID := strings.TrimSpace(*acc.ProviderRef)
	if caID == "" {
		return "", fmt.Errorf("composio: account %q has no connected account id (provider_ref is empty)", acc.Handle)
	}
	return caID, nil
}

// parseIGTime reads the timestamp Instagram puts on a media object. It arrives
// as "2026-09-25T23:46:19+0000" — RFC3339 with the zone colon missing, which
// time.RFC3339 rejects. A post whose date cannot be read is still worth
// storing; only the sort order degrades, so this returns nil rather than an
// error.
func parseIGTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	if t, err := time.Parse("2006-01-02T15:04:05-0700", s); err == nil {
		return &t
	}
	return nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// entityID resolves the Composio user that owns a connection, memoized per
// process because it is immutable for a connection's lifetime.
func (c *Composio) entityID(ctx context.Context, caID string) (string, error) {
	if v, ok := c.entityCache.Load(caID); ok {
		return v.(string), nil
	}
	var detail struct {
		UserID string `json:"user_id"`
	}
	if err := c.get(ctx, "/connected_accounts/"+caID, &detail); err != nil {
		return "", fmt.Errorf("composio: resolve owner of %s: %w", caID, err)
	}
	if detail.UserID == "" {
		return "", fmt.Errorf("composio: connection %s reports no owner", caID)
	}
	c.entityCache.Store(caID, detail.UserID)
	return detail.UserID, nil
}

// execute calls one toolkit tool and unwraps Composio's {data, successful,
// error} envelope. A non-2xx or successful:false becomes a Go error carrying
// the status code, which is what the ingestor's error classifier reads to tell
// a rate limit from a bad key.
func (c *Composio) execute(ctx context.Context, slug, caID, entity string, args map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{
		"connected_account_id": caID,
		"entity_id":            entity,
		"arguments":            args,
	})
	if err != nil {
		return fmt.Errorf("composio: encode %s: %w", slug, err)
	}
	var envelope struct {
		Data       json.RawMessage `json:"data"`
		Successful bool            `json:"successful"`
		Error      any             `json:"error"`
	}
	if err := c.do(ctx, http.MethodPost, "/tools/execute/"+slug, body, &envelope); err != nil {
		return fmt.Errorf("composio: %s: %w", slug, err)
	}
	if !envelope.Successful {
		return fmt.Errorf("composio: %s failed: %v", slug, envelope.Error)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("composio: %s: decode payload: %w", slug, err)
	}
	return nil
}

func (c *Composio) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Composio) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The status code is part of the message on purpose: the ingestor
		// classifies runs by substring (401 -> AUTH, 429 -> RATE_LIMIT).
		return fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// intPtr reads a metric Instagram may simply not have reported. A missing
// metric must never become 0: the dashboard skips nil columns so an
// unreported metric renders as "—" instead of a fabricated number.
func intPtr(values map[string]int64, name string) *int64 {
	v, ok := values[name]
	if !ok {
		return nil
	}
	return &v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
