package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// ScrapeIngestor (P2-04) is the second half of the scrape pipeline: it reads
// the raw Apify dataset items back out of object storage and normalizes them
// into Postgres rows. It runs after a scrape job succeeds.
//
// Idempotence is the contract (TICKETS P2-04 AC): re-running the ingest for a
// run must not change row counts. Every upsert is keyed on
// (platform, external_id), and metric snapshots are pure appends whose value
// comes from the payload, so a replay refreshes the same rows.
type ScrapeIngestor struct {
	scrapes port.ScrapeStore
	storage port.RawStorage
	// platform picks the per-actor wire-shape parser (parseItems). Empty keeps
	// the legacy self-describing contract.
	platform domain.Platform
	// window, when set, drops search posts whose taken_at falls outside it. The
	// search actors ignore the since/until we send, so the ingestor applies the
	// filter itself. Zero = no filter.
	window port.KeywordTimeWindow
	log    *slog.Logger
}

// NewScrapeIngestor wires the ingestor.
func NewScrapeIngestor(store port.ScrapeStore, storage port.RawStorage, logger *slog.Logger) *ScrapeIngestor {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScrapeIngestor{scrapes: store, storage: storage, log: logger}
}

// NewPlatformScrapeIngestor wires the ingestor for one actor's wire shape.
func NewPlatformScrapeIngestor(store port.ScrapeStore, storage port.RawStorage, p domain.Platform, logger *slog.Logger) *ScrapeIngestor {
	ing := NewScrapeIngestor(store, storage, logger)
	ing.platform = p
	return ing
}

// NewSearchScrapeIngestor wires the ingestor for a keyword-search run: the
// platform's actor shape plus the date window the search must be filtered by
// (the actors ignore since/until, so the ingestor applies it on the posts).
func NewSearchScrapeIngestor(store port.ScrapeStore, storage port.RawStorage, p domain.Platform, window port.KeywordTimeWindow, logger *slog.Logger) *ScrapeIngestor {
	ing := NewPlatformScrapeIngestor(store, storage, p, logger)
	ing.window = window
	return ing
}

var _ port.ScrapeIngestor = (*ScrapeIngestor)(nil)

// IngestRun reads every raw payload key of one run and normalizes it. A run
// with zero items is a success with zero counts (an empty scrape is data, not
// an error). A payload that cannot be parsed is skipped with a warning — one
// malformed item must never block the well-formed ones.
func (s *ScrapeIngestor) IngestRun(ctx context.Context, runID string) (port.ScrapeIngestResult, error) {
	if s.storage == nil {
		return port.ScrapeIngestResult{}, errors.New("ingestor: storage is required")
	}
	keys, err := s.scrapes.ListRawPayloadsByRunID(ctx, runID)
	if err != nil {
		return port.ScrapeIngestResult{}, fmt.Errorf("ingestor: list payloads: %w", err)
	}

	var res port.ScrapeIngestResult
	for _, key := range keys {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		raw, err := s.storage.Get(ctx, key)
		if err != nil {
			s.log.Warn("ingestor: payload read failed", "key", key, "err", err)
			continue
		}
		res.BytesIngested += int64(len(raw))

		items, err := parseItems(s.platform, raw, s.window)
		if err != nil {
			s.log.Warn("ingestor: payload parse failed", "key", key, "err", err)
			continue
		}
		for _, item := range items {
			if err := s.ingestItem(ctx, item, &res); err != nil {
				s.log.Warn("ingestor: item skipped", "key", key, "err", err)
			}
		}
	}
	s.log.Info("ingestor: run complete", "run", runID, "posts", res.PostsUpserted,
		"comments", res.CommentsUpserted, "snapshots", res.SnapshotsWritten,
		"bytes", res.BytesIngested)
	return res, nil
}

// ingestItem normalizes one actor dataset item into domain entities and writes
// them. A comment with a postId resolves to that post; an item without one is
// treated as a post (the actor's top-level shape).
func (s *ScrapeIngestor) ingestItem(ctx context.Context, item scrapeItem, res *port.ScrapeIngestResult) error {
	p := domain.Platform(item.Platform)
	if !p.Valid() {
		return fmt.Errorf("unknown platform %q", item.Platform)
	}
	if item.ExternalID == "" {
		return errors.New("item has no external id")
	}

	if item.PostID != "" || item.CommentOf != "" {
		return s.ingestComment(ctx, p, item, res)
	}
	return s.ingestPost(ctx, p, item, res)
}

func (s *ScrapeIngestor) ingestPost(ctx context.Context, p domain.Platform, item scrapeItem, res *port.ScrapeIngestResult) error {
	post, err := s.scrapes.UpsertPost(ctx, domain.Post{
		Platform:     p,
		ExternalID:   item.ExternalID,
		AuthorHandle: orDefault(item.AuthorHandle, "unknown"),
		AuthorID:     orDefault(item.AuthorID, item.ExternalID),
		Text:         nilIfEmpty(item.Text),
		MediaURLs:    item.MediaURLs,
		Metrics:      item.metricsJSON(),
	})
	if err != nil {
		return fmt.Errorf("upsert post: %w", err)
	}
	res.PostsUpserted++

	// A metric sample per ingest keeps the history hypertable fed.
	if err := s.scrapes.CreateMetricSnapshot(ctx, domain.MetricSnapshot{
		PostID:   post.ID,
		TS:       time.Now().UTC(),
		Views:    item.metricsInt("views", "viewCount", "playCount"),
		Likes:    item.metricsInt("likes", "likeCount"),
		Comments: item.metricsInt("comments", "commentCount"),
		Shares:   item.metricsInt("shares", "shareCount", "retweetCount"),
		Reach:    item.metricsIntPtr("reach", "impressions"),
	}); err != nil {
		return fmt.Errorf("metric snapshot: %w", err)
	}
	res.SnapshotsWritten++
	return nil
}

func (s *ScrapeIngestor) ingestComment(ctx context.Context, p domain.Platform, item scrapeItem, res *port.ScrapeIngestResult) error {
	// A comment needs a parent post. The actor may send the post id directly
	// (item.PostID) or the external id of the parent (item.CommentOf).
	postID := item.PostID
	if postID == "" {
		parent, err := s.scrapes.GetPostByExternalID(ctx, p, item.CommentOf)
		if err != nil {
			return fmt.Errorf("parent post %q: %w", item.CommentOf, err)
		}
		postID = parent.ID
	}
	_, err := s.scrapes.UpsertComment(ctx, domain.Comment{
		PostID:       postID,
		Platform:     p,
		ExternalID:   item.ExternalID,
		AuthorHandle: orDefault(item.AuthorHandle, "unknown"),
		Text:         orDefault(item.Text, ""),
		Metrics:      item.metricsJSON(),
	})
	if err != nil {
		return fmt.Errorf("upsert comment: %w", err)
	}
	res.CommentsUpserted++
	return nil
}

// --- helpers ----------------------------------------------------------------

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// scrapeItem is the loose shape of an Apify dataset row, normalized from the
// per-actor wire shapes via parseItem. The fields are the domain's canonical
// names; the actors' own field names (shortCode vs post_code, caption vs
// text_content, ...) live only in parseItem, so a new actor is a one-function
// addition instead of an ingest rewrite.
type scrapeItem struct {
	Platform     string         `json:"platform"`
	ExternalID   string         `json:"externalId"`
	PostID       string         `json:"postId"`
	CommentOf    string         `json:"commentOf"`
	AuthorHandle string         `json:"authorHandle"`
	AuthorID     string         `json:"authorId"`
	Text         string         `json:"text"`
	MediaURLs    []string       `json:"mediaUrls"`
	Metrics      map[string]any `json:"metrics"`
}

// igCommentItem is one element of instagram-post-scraper's latestComments[].
type igCommentItem struct {
	ID            string `json:"id"`
	Text          string `json:"text"`
	OwnerUsername string `json:"ownerUsername"`
	LikesCount    *int64 `json:"likesCount"`
}

// parseItems translates one raw dataset row into the canonical scrapeItems it
// yields. Most actors emit one post per row, but the Instagram SEARCH actor
// emits one row per resolved page (a location/hashtag page) with the posts
// nested under posts[] — one row, many items. platform tells the parser which
// wire shape to expect; the raw row carries no platform field of its own.
//
// Error rows the actors emit for unreachable targets ({"error": "no_items"},
// "not_found", "restricted_page") parse to an error here, so the caller skips
// them instead of storing a junk post keyed by an error message.
func parseItems(platform domain.Platform, raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
	switch platform {
	case domain.PlatformInstagram:
		return parseIGItems(raw, window)
	case domain.PlatformThreads:
		item, err := parseThreadsItem(raw)
		if err != nil {
			return nil, err
		}
		return []scrapeItem{item}, nil
	}
	// Unknown actor shape: keep the legacy self-describing contract.
	item, err := parseLegacyItem(raw)
	if err != nil {
		return nil, err
	}
	return []scrapeItem{item}, nil
}

// parseLegacyItem is the self-describing contract from before per-actor
// parsers existed (tests and any actor without a dedicated parser).
func parseLegacyItem(raw []byte) (scrapeItem, error) {
	var item scrapeItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return scrapeItem{}, err
	}
	if item.ExternalID == "" {
		return scrapeItem{}, errors.New("item has no external id")
	}
	return item, nil
}

// parseIGItems dispatches between the two Instagram wire shapes: the
// post-scraper's row (one post, comments riding on latestComments[]) and the
// search actor's page row (posts nested under posts[]). A row carrying
// searchTerm/posts[] is a search page; anything else is a post row.
func parseIGItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
	var probe struct {
		SearchTerm *string           `json:"searchTerm"`
		Posts      []json.RawMessage `json:"posts"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	if probe.SearchTerm != nil || probe.Posts != nil {
		return parseIGSearchItems(raw, window)
	}
	return parseIGPostItems(raw)
}

// parseIGPostItems maps apify/instagram-post-scraper's post row: the post
// itself plus the latestComments[] that ride on the same row. Each comment is
// keyed to the post's shortcode, so the ingestor stores the post first and the
// comments resolve their parent through it.
func parseIGPostItems(raw []byte) ([]scrapeItem, error) {
	var src struct {
		ID             string          `json:"id"`
		ShortCode      string          `json:"shortCode"`
		OwnerUsername  string          `json:"ownerUsername"`
		OwnerID        any             `json:"ownerId"`
		Caption        string          `json:"caption"`
		FirstComment   string          `json:"firstComment"`
		Images         []string        `json:"images"`
		DisplayURL     string          `json:"displayUrl"`
		URL            string          `json:"url"`
		Type           string          `json:"type"`
		LikesCount     *int64          `json:"likesCount"`
		CommentsCount  *int64          `json:"commentsCount"`
		VideoViewCount *int64          `json:"videoViewCount"`
		Timestamp      string          `json:"timestamp"`
		LatestComments []igCommentItem `json:"latestComments"`
		Error          string          `json:"error"`
		ErrorDesc      string          `json:"errorDescription"`
	}
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	if src.Error != "" {
		return nil, fmt.Errorf("actor error %q: %s", src.Error, src.ErrorDesc)
	}
	id := src.ShortCode
	if id == "" {
		id = src.ID
	}
	if id == "" {
		return nil, errors.New("item has no shortcode")
	}
	media := src.Images
	if len(media) == 0 && src.DisplayURL != "" {
		media = []string{src.DisplayURL}
	}
	metrics := map[string]any{
		"likes":    src.LikesCount,
		"comments": src.CommentsCount,
	}
	if src.VideoViewCount != nil {
		metrics["views"] = src.VideoViewCount
	}
	post := scrapeItem{
		Platform:     string(domain.PlatformInstagram),
		ExternalID:   id,
		AuthorHandle: orDefault(src.OwnerUsername, "unknown"),
		AuthorID:     anyToString(src.OwnerID),
		Text:         orDefault(src.Caption, src.FirstComment),
		MediaURLs:    media,
		Metrics:      metrics,
	}
	items := []scrapeItem{post}
	items = append(items, parseIGComments(domain.PlatformInstagram, post, src.LatestComments)...)
	return items, nil
}

// parseIGSearchItems maps the Instagram search actor's page row: one row per
// resolved location/hashtag page, the posts nested under posts[]. A page that
// resolved but holds no posts (a small hashtag page, postsCount: 0) yields
// zero items — a successful empty search, not an error. The window is applied
// here because the search actor ignores the since/until input we send.
func parseIGSearchItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
	var row struct {
		SearchTerm   string            `json:"searchTerm"`
		SearchSource string            `json:"searchSource"`
		Name         string            `json:"name"`
		Posts        []json.RawMessage `json:"posts"`
		Error        string            `json:"error"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, err
	}
	if row.Error != "" {
		return nil, fmt.Errorf("actor error %q", row.Error)
	}
	items := make([]scrapeItem, 0, len(row.Posts))
	for _, p := range row.Posts {
		post, err := parseIGSearchPost(p)
		if err != nil {
			// One unreadable post must not cost the whole page.
			continue
		}
		if !inWindow(post.takenAt, window) {
			continue
		}
		items = append(items, post.item)
	}
	return items, nil
}

// parseIGSearchPost maps one element of a search page row's posts[]. The
// fields are the raw Instagram media-dict names the actor passes through
// (code, taken_at, caption.text, image_versions2.candidates, carousel_media).
type igSearchPost struct {
	item    scrapeItem
	takenAt time.Time
}

func parseIGSearchPost(raw []byte) (igSearchPost, error) {
	var p struct {
		Code    string `json:"code"`
		Pk      any    `json:"pk"`
		Caption struct {
			Text      string `json:"text"`
			CreatedAt int64  `json:"created_at"`
		} `json:"caption"`
		TakenAt       int64 `json:"taken_at"`
		LikeCount     *int64 `json:"like_count"`
		CommentCount  *int64 `json:"comment_count"`
		ViewCount     *int64 `json:"view_count"`
		PlayCount     *int64 `json:"play_count"`
		User          struct {
			Username string `json:"username"`
			Pk       any    `json:"pk"`
		} `json:"user"`
		DisplayURI     string `json:"display_uri"`
		ImageVersions2 *struct {
			Candidates []struct {
				URL string `json:"url"`
			} `json:"candidates"`
		} `json:"image_versions2"`
		CarouselMedia []struct {
			ImageVersions2 *struct {
				Candidates []struct {
					URL string `json:"url"`
				} `json:"candidates"`
			} `json:"image_versions2"`
			VideoVersions []struct {
				URL string `json:"url"`
			} `json:"video_versions"`
		} `json:"carousel_media"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return igSearchPost{}, err
	}
	if p.Code == "" {
		return igSearchPost{}, errors.New("search post has no code")
	}
	// Media: the first candidate is the largest variant. A carousel carries one
	// image or video per carousel_media entry; a single-media post carries it
	// on the row itself.
	media := make([]string, 0)
	if len(p.CarouselMedia) > 0 {
		for _, c := range p.CarouselMedia {
			if c.ImageVersions2 != nil && len(c.ImageVersions2.Candidates) > 0 {
				media = append(media, c.ImageVersions2.Candidates[0].URL)
			}
			if len(c.VideoVersions) > 0 {
				media = append(media, c.VideoVersions[0].URL)
			}
		}
	} else if p.ImageVersions2 != nil && len(p.ImageVersions2.Candidates) > 0 {
		media = append(media, p.ImageVersions2.Candidates[0].URL)
	}
	if len(media) == 0 && p.DisplayURI != "" {
		media = []string{p.DisplayURI}
	}
	metrics := map[string]any{
		"likes":    p.LikeCount,
		"comments": p.CommentCount,
	}
	// Reels carry play_count instead of view_count; count either as views.
	if p.ViewCount != nil {
		metrics["views"] = p.ViewCount
	} else if p.PlayCount != nil {
		metrics["views"] = p.PlayCount
	}
	item := scrapeItem{
		Platform:     string(domain.PlatformInstagram),
		ExternalID:   p.Code,
		AuthorHandle: orDefault(p.User.Username, "unknown"),
		AuthorID:     anyToString(p.User.Pk),
		Text:         p.Caption.Text,
		MediaURLs:    media,
		Metrics:      metrics,
	}
	return igSearchPost{item: item, takenAt: time.Unix(p.TakenAt, 0).UTC()}, nil
}

// inWindow reports whether a post's taken_at falls inside the operator's
// filter. A zero window is "no filter"; a post with no timestamp is kept
// rather than dropped on a missing field (the actor's gap, not the data's).
func inWindow(t time.Time, w port.KeywordTimeWindow) bool {
	if w.From.IsZero() && w.To.IsZero() {
		return true
	}
	if t.IsZero() {
		return true
	}
	if !w.From.IsZero() && t.Before(w.From) {
		return false
	}
	if !w.To.IsZero() && !t.Before(w.To) {
		return false
	}
	return true
}

// parseIGComments splits a post row into one scrapeItem per latestComments
// entry (each keyed to the parent post's shortcode). The post itself is
// emitted first so the parent row exists when the comments resolve it.
func parseIGComments(platform domain.Platform, post scrapeItem, comments []igCommentItem) []scrapeItem {
	items := make([]scrapeItem, 0, len(comments))
	for _, c := range comments {
		if c.ID == "" || strings.TrimSpace(c.Text) == "" {
			continue
		}
		items = append(items, scrapeItem{
			Platform:     string(platform),
			ExternalID:   c.ID,
			CommentOf:    post.ExternalID,
			AuthorHandle: orDefault(c.OwnerUsername, "unknown"),
			Text:         c.Text,
			Metrics:      map[string]any{"likes": c.LikesCount},
		})
	}
	return items
}

// parseThreadsItem maps futurizerush/meta-threads-scraper's post row.
func parseThreadsItem(raw []byte) (scrapeItem, error) {
	var src struct {
		PostCode   string `json:"post_code"`
		PostURL    string `json:"post_url"`
		Username   string `json:"username"`
		UserID     any    `json:"user_id"`
		Text       string `json:"text_content"`
		MediaURLs  []string `json:"media_urls"`
		MediaType  string `json:"media_type"`
		CreatedAt  string `json:"created_at"`
		LikeCount  *int64 `json:"like_count"`
		ReplyCount *int64 `json:"reply_count"`
		RepostCount *int64 `json:"repost_count"`
		QuoteCount *int64 `json:"quote_count"`
		ShareCount *int64 `json:"share_count"`
		ViewCount  *int64 `json:"view_count"`
		IsReply    bool   `json:"is_reply"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(raw, &src); err != nil {
		return scrapeItem{}, err
	}
	if src.Error != "" {
		return scrapeItem{}, fmt.Errorf("actor error %q", src.Error)
	}
	if src.PostCode == "" {
		return scrapeItem{}, errors.New("item has no post_code")
	}
	media := src.MediaURLs
	metrics := map[string]any{
		"likes":    src.LikeCount,
		"comments": src.ReplyCount,
		"shares":   orInt(src.ShareCount, src.RepostCount, src.QuoteCount),
	}
	if src.ViewCount != nil {
		metrics["views"] = src.ViewCount
	}
	return scrapeItem{
		Platform:     string(domain.PlatformThreads),
		ExternalID:   src.PostCode,
		AuthorHandle: orDefault(src.Username, "unknown"),
		AuthorID:     anyToString(src.UserID),
		Text:         src.Text,
		MediaURLs:    media,
		Metrics:      metrics,
	}, nil
}

// anyToString coerces an actor id that arrives as number-or-string into a
// string (both shapes occur across actor builds).
func anyToString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return strconv.FormatInt(int64(s), 10)
	case json.Number:
		return string(s)
	}
	return ""
}

// orInt returns the first non-nil of the candidates.
func orInt(vals ...*int64) *int64 {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

// metricsJSON returns the item's metrics as a non-empty jsonb payload.
func (i scrapeItem) metricsJSON() []byte {
	if len(i.Metrics) == 0 {
		return []byte("{}")
	}
	b, err := json.Marshal(i.Metrics)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// metricsInt reads one of several candidate keys as a non-negative int64.
func (i scrapeItem) metricsInt(keys ...string) int64 {
	for _, k := range keys {
		if v, ok := numeric(i.Metrics[k]); ok {
			return v
		}
	}
	return 0
}

func (i scrapeItem) metricsIntPtr(keys ...string) *int64 {
	for _, k := range keys {
		if v, ok := numeric(i.Metrics[k]); ok {
			return &v
		}
	}
	return nil
}

// numeric coerces a JSON number-ish value to int64. Apify datasets send numbers
// as floats and sometimes as strings; both are accepted, anything else is not.
func numeric(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case string:
		// best-effort; a non-numeric string yields false
		var f float64
		if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
			return int64(f), true
		}
	}
	return 0, false
}
