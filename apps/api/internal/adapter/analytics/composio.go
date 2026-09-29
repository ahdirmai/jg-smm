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
