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
	// onlyCode, when set, drops every post whose code is not this one. The
	// Threads post-actor returns share/quote CONTEXT rows alongside the
	// requested post — auto-generated "Dengan <name> – Saya baru saja diakui…"
	// text, `is_reply:false`, `parent_post_id` absent — with no field that
	// separates them from the post the operator asked for. Without this
	// allowlist they land in the post table as ordinary posts.
	// Empty = accept every post (the keyword path).
	onlyCode string
	log      *slog.Logger
}

// NewScrapeIngestor wires the ingestor.
func NewScrapeIngestor(store port.ScrapeStore, storage port.RawStorage, logger *slog.Logger) *ScrapeIngestor {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScrapeIngestor{scrapes: store, storage: storage, log: logger}
}

// NewPlatformScrapeIngestor wires the ingestor for one actor's wire shape.
// onlyCode, when non-empty, is the one post code a permalink scrape accepts;
// see the field's comment for why the context rows must be dropped.
func NewPlatformScrapeIngestor(store port.ScrapeStore, storage port.RawStorage, p domain.Platform, onlyCode string, logger *slog.Logger) *ScrapeIngestor {
	ing := NewScrapeIngestor(store, storage, logger)
	ing.platform = p
	ing.onlyCode = onlyCode
	return ing
}

// NewSearchScrapeIngestor wires the ingestor for a keyword-search run: the
// platform's actor shape plus the date window the search must be filtered by
// (the actors ignore since/until, so the ingestor applies it on the posts).
func NewSearchScrapeIngestor(store port.ScrapeStore, storage port.RawStorage, p domain.Platform, window port.KeywordTimeWindow, logger *slog.Logger) *ScrapeIngestor {
	ing := NewPlatformScrapeIngestor(store, storage, p, "", logger)
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
	// Threads replies name their parent by post id rather than nesting under it,
	// so a run-scoped map joins them. Only Threads builds it: the other shapes
	// are self-contained per row.
	var run *threadsRun
	if s.platform == domain.PlatformThreads {
		run = newThreadsRun()
	}
	// Keys arrive in `received_at` order (ListRawPayloadsByRun), and the actor
	// emits a post before its replies, so a reply's parent is already in `run`
	// by the time the reply is parsed.
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

		items, err := parseItems(s.platform, raw, s.window, s.onlyCode, run)
		if err != nil {
			s.log.Warn("ingestor: payload parse failed", "key", key, "err", err)
			continue
		}
		// "items read" is what the actor delivered, and parseItems drops
		// out-of-window rows internally — so len(items) made itemsRead identical
		// to postsCount by construction, and a batch that fetched 150 and stored
		// 27 reported 27/27. The operator then widened maxPosts and burned more
		// compute, the exact opposite of the right advice. The same parse with
		// the window opened recovers the delivered count.
		read := len(items)
		if unfiltered, err := parseItems(s.platform, raw, port.KeywordTimeWindow{}, s.onlyCode, run); err == nil {
			read = len(unfiltered)
		}
		res.ItemsRead += read
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
		PublishedAt:  item.PublishedAt,
	})
	if err != nil {
		return fmt.Errorf("upsert post: %w", err)
	}
	res.PostsUpserted++
	res.PostIDs = append(res.PostIDs, post.ID)

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
	// A reply names its parent by the parent comment's external id. The parent
	// row was written first (parseIGComments walks depth-first), so this lookup
	// succeeds; a parent we somehow do not have stores the reply as top-level
	// rather than dropping it — a reply without its parent is still real
	// content, and the alternative (skip) loses it silently.
	var parentID *string
	if item.ParentExternalID != "" {
		parent, err := s.scrapes.GetCommentByExternalID(ctx, p, item.ParentExternalID)
		if err != nil {
			s.log.Warn("ingest: reply parent not found, storing as top-level",
				"reply", item.ExternalID, "parent", item.ParentExternalID, "err", err)
		} else {
			parentID = &parent.ID
		}
	}

	_, err := s.scrapes.UpsertComment(ctx, domain.Comment{
		PostID:       postID,
		Platform:     p,
		ExternalID:   item.ExternalID,
		AuthorHandle: orDefault(item.AuthorHandle, "unknown"),
		Text:         orDefault(item.Text, ""),
		Metrics:      item.metricsJSON(),
		ParentID:     parentID,
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
	Platform   string `json:"platform"`
	ExternalID string `json:"externalId"`
	PostID     string `json:"postId"`
	CommentOf  string `json:"commentOf"`
	// ParentExternalID is the enclosing comment's external id when this item is
	// a reply, empty for a top-level comment. The DB stores parent as a uuid,
	// so the ingestor resolves this to comment.parent_id after the parent row
	// is written (replies are emitted depth-first so it already exists).
	ParentExternalID string         `json:"parentExternalId"`
	AuthorHandle     string         `json:"authorHandle"`
	AuthorID         string         `json:"authorId"`
	Text             string         `json:"text"`
	MediaURLs        []string       `json:"mediaUrls"`
	Metrics          map[string]any `json:"metrics"`
	// PublishedAt carries the post's own publish time through to the DB. It is
	// the same value the window filter already read (takenAt) — previously it
	// was used to decide keep/drop and then thrown away, so no post row ever
	// carried a date.
	PublishedAt *time.Time `json:"publishedAt"`
}

// igCommentItem is one element of instagram-post-scraper's latestComments[] or
// apify/instagram-comment-scraper's dataset row. Both carry the same core
// fields; Replies is populated only by the comment scraper with
// includeNestedComments, which is gated to paid Apify plans.
//
// The thread shape is NESTED, not flat: a reply sits in its parent's Replies
// slice and does NOT appear as its own top-level row. That is why parsing must
// recurse — reading only the top level silently drops every reply, which looks
// identical to "this post has no replies".
type igCommentItem struct {
	ID            string `json:"id"`
	Text          string `json:"text"`
	OwnerUsername string `json:"ownerUsername"`
	LikesCount    *int64 `json:"likesCount"`
	// RepliesCount is the platform's own count. It is NOT used to join
	// anything; it is kept only so a reply-less payload can be told apart from
	// a payload whose replies the plan stripped.
	RepliesCount *int64          `json:"repliesCount"`
	Replies      []igCommentItem `json:"replies"`
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
//
// run carries the per-run state the Threads shape needs to link a reply to its
// parent, and is nil for every other caller. Each dataset item is parsed on its
// own with no view of its siblings, but a Threads reply references its parent by
// numeric id while the post row is keyed by shortcode — so the two can only be
// joined by remembering what the run has already emitted.
func parseItems(platform domain.Platform, raw []byte, window port.KeywordTimeWindow, onlyCode string, run *threadsRun) ([]scrapeItem, error) {
	switch platform {
	case domain.PlatformInstagram:
		return parseIGItems(raw, window)
	case domain.PlatformThreads:
		return parseThreadsItem(raw, window, onlyCode, run)
	case domain.PlatformTikTok:
		return parseTikTokItems(raw, window)
	case domain.PlatformYouTube:
		return parseYouTubeItems(raw, window)
	case domain.PlatformLinkedIn:
		return parseLinkedInItems(raw, window)
	case domain.PlatformX:
		return parseXItems(raw, window)
	case domain.PlatformFacebook:
		return parseFacebookItems(raw, window)
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

// parseIGItems dispatches between the three Instagram wire shapes: the
// post-scraper's row (one post, comments riding on latestComments[]), the
// search actor's page row (posts nested under posts[]), and the
// crawlerbros keyword-search row (one post, snake_case, no comments). A row
// carrying searchTerm/posts[] is a search page; anything else is a post row.
func parseIGItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
	// Posts is RawMessage, not []RawMessage: a hashtag page row carries `posts`
	// as a formatted count ("440.01 K"), and unmarshalling that into a slice
	// fails the WHOLE row. A row that is only page metadata yields zero items.
	var probe struct {
		SearchTerm *string         `json:"searchTerm"`
		Posts      json.RawMessage `json:"posts"`
		// InputKeyword is the flat actor's only mandatory field. It is the probe
		// because shortCode/shortcode are indistinguishable to Go's
		// case-insensitive unmarshal — only the underscore separates the two
		// shapes, and it is exactly the part a camelCase struct cannot read.
		// Probing on comment_count instead would be fragile: a post with no
		// comments may omit the key, and the row would fall through to the
		// wrong parser and come back with null metrics again.
		InputKeyword *string `json:"input_keyword"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	if probe.SearchTerm == nil && len(probe.Posts) == 0 {
		if probe.InputKeyword != nil {
			return parseIGFlatPostItems(raw, window)
		}
		return parseIGPostItems(raw, window)
	}
	if len(probe.Posts) == 0 || probe.Posts[0] != '[' {
		return nil, nil
	}
	return parseIGSearchItems(raw, window)
}

// parseIGFlatPostItems maps crawlerbros/instagram-keyword-search-scraper's row:
// ONE post per dataset item, flat, snake_case. This shape is close enough to the
// post-scraper's that the dispatch is subtle: it has no searchTerm and no posts[]
// (so parseIGItems routes it here rather than to the page parser), but its fields
// are snake_case, and Go's case-insensitive unmarshal cannot bridge `like_count`
// to `likesCount` — an underscore is not a case difference. Reading this row
// through the post-scraper struct yields a post with `likes: null, comments: null`
// and handle `unknown`, which is why the batch dashboard showed a comment count
// of nothing at all. Hence a separate parser rather than alias fields.
//
// The window is applied here for the same reason as everywhere else: this actor
// accepts no date input, so the operator's filter can only be enforced post-hoc.
//
// It returns no comments: the actor exposes comment_count but never the comment
// bodies, so `Fetch comments` in the dashboard has to run the post scraper.
func parseIGFlatPostItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
	var src struct {
		Shortcode  string `json:"shortcode"`
		Username   string `json:"username"`
		AuthorMeta struct {
			ID  any    `json:"id"`
			Bio string `json:"bio"`
		} `json:"author_meta"`
		Caption      string `json:"caption"`
		PubDate      string `json:"pub_date"`
		LikeCount    *int64 `json:"like_count"`
		CommentCount *int64 `json:"comment_count"`
		ViewCount    *int64 `json:"view_count"`
		PlayCount    *int64 `json:"play_count"`
		MediaItems   []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"media_items"`
		ThumbnailURL string `json:"thumbnail_url"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	if src.Error != "" {
		return nil, fmt.Errorf("actor error: %s", src.Error)
	}
	if src.Shortcode == "" {
		return nil, errors.New("item has no shortcode")
	}
	takenAt := parseActorTime(src.PubDate)
	if !inWindow(takenAt, window) {
		return nil, nil
	}
	media := make([]string, 0, len(src.MediaItems))
	for _, m := range src.MediaItems {
		if m.URL != "" {
			media = append(media, m.URL)
		}
	}
	if len(media) == 0 && src.ThumbnailURL != "" {
		media = []string{src.ThumbnailURL}
	}
	metrics := map[string]any{
		"likes":    src.LikeCount,
		"comments": src.CommentCount,
	}
	// Reels report play_count; posts report like_count only. Count either as views.
	if src.ViewCount != nil {
		metrics["views"] = src.ViewCount
	} else if src.PlayCount != nil {
		metrics["views"] = src.PlayCount
	}
	return []scrapeItem{{
		Platform:     string(domain.PlatformInstagram),
		ExternalID:   src.Shortcode,
		AuthorHandle: orDefault(src.Username, "unknown"),
		AuthorID:     anyToString(src.AuthorMeta.ID),
		Text:         src.Caption,
		MediaURLs:    media,
		Metrics:      metrics,
		PublishedAt:  publishedAtPtr(takenAt),
	}}, nil
}

// tiktokPost is one dataset row from xmolodtsov/tiktok-search-scraper. The
// author and the media are each a nested object, so this platform still needs
// its own parser — there is no underscore/camel bridging to save us here.
type tiktokPost struct {
	ID   string `json:"id"`
	Text string `json:"title"`
	// UploadedAtFormatted is ISO and is what this actor always sends;
	// UploadedAt is the same instant as Unix seconds and is the fallback.
	UploadedAtFormatted string `json:"uploadedAtFormatted"`
	UploadedAt          int64  `json:"uploadedAt"`
	// Pointers so a genuine 0 is stored and an absent field is not. A plain int
	// plus a `!= 0` guard conflates the two, which made TikTok serialise a real
	// zero differently from the other six parsers.
	Views     *int64 `json:"views"`
	Likes     *int64 `json:"likes"`
	Comments  *int64 `json:"comments"`
	Shares    *int64 `json:"shares"`
	Bookmarks *int64 `json:"bookmarks"`
	Video     struct {
		// Both are plain TikTok CDN URLs present on every row, and neither is
		// behind a billed flag: cover is the full-size frame, thumbnail the
		// smaller one. cover is preferred.
		Cover     string `json:"cover"`
		Thumbnail string `json:"thumbnail"`
	} `json:"video"`
	Channel struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"channel"`
}

// parseTikTokItems maps one xmolodtsov/tiktok-search-scraper dataset row.
//
// The window is applied here and nowhere else: this actor has no date input at
// all, so unlike the previous one there is no server-side filter to lean on and
// a window the payload cannot express is a window this function must enforce.
func parseTikTokItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
	var src tiktokPost
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	if src.ID == "" {
		return nil, errors.New("tiktok item has no id")
	}
	publishedAt := parseActorTime(src.UploadedAtFormatted)
	if publishedAt.IsZero() && src.UploadedAt != 0 {
		publishedAt = time.Unix(src.UploadedAt, 0).UTC()
	}
	if !inWindow(publishedAt, window) {
		return nil, nil
	}
	// Non-nil even when empty: this list is stored and served as a JSON array,
	// and a null there is a different thing to every reader downstream.
	media := make([]string, 0, 1)
	if cover := orDefault(src.Video.Cover, src.Video.Thumbnail); cover != "" {
		media = append(media, cover)
	}
	metrics := map[string]any{}
	if src.Likes != nil {
		metrics["likes"] = *src.Likes
	}
	if src.Comments != nil {
		metrics["comments"] = *src.Comments
	}
	if src.Shares != nil {
		metrics["shares"] = *src.Shares
	}
	if src.Bookmarks != nil {
		// Stored under the same key the previous actor's `collectCount` used.
		// TikTok's save count is the same measure under the other name, and
		// renaming the key would orphan the value on every stored post.
		metrics["collects"] = *src.Bookmarks
	}
	if src.Views != nil {
		// `views`, not a new `plays` key. The snapshot writer reads
		// metricsInt("views", "viewCount", "playCount") — an unfamiliar key is
		// stored and then read by nobody, which is a stored view count of zero
		// on a post that otherwise looks entirely correct.
		metrics["views"] = *src.Views
	}
	return []scrapeItem{{
		Platform:     string(domain.PlatformTikTok),
		ExternalID:   src.ID,
		AuthorHandle: orDefault(src.Channel.Username, "unknown"),
		AuthorID:     src.Channel.ID,
		Text:         src.Text,
		MediaURLs:    media,
		Metrics:      metrics,
		PublishedAt:  publishedAtPtr(publishedAt),
	}}, nil
}

// parseIGPostItems maps apify/instagram-post-scraper's post row: the post
// itself plus the latestComments[] that ride on the same row. Each comment is
// keyed to the post's shortcode, so the ingestor stores the post first and the
// comments resolve their parent through it.
//
// This is also the shape a hashtag keyword search returns (apify/instagram-
// scraper with searchType:hashtag), one flat row per post, so the window is
// applied here too: that actor's only date bound is a lower one.
func parseIGPostItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
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
	// The hashtag search's upper bound has no actor-side equivalent
	// (onlyPostsNewerThan is a lower bound only), so drop the tail here. The
	// post-scrape path passes a zero window and is unaffected.
	takenAt := parseActorTime(src.Timestamp)
	if !inWindow(takenAt, window) {
		return nil, nil
	}
	// Copied, not aliased: an omitted or null `images` leaves src.Images nil,
	// and a nil MediaURLs is marshalled as JSON null — the thing every other
	// site guards against with make([]string, 0, n).
	media := make([]string, 0, len(src.Images)+1)
	media = append(media, src.Images...)
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
		PublishedAt:  publishedAtPtr(takenAt),
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
		TakenAt      int64  `json:"taken_at"`
		LikeCount    *int64 `json:"like_count"`
		CommentCount *int64 `json:"comment_count"`
		ViewCount    *int64 `json:"view_count"`
		PlayCount    *int64 `json:"play_count"`
		User         struct {
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
	takenAt := time.Unix(p.TakenAt, 0).UTC()
	item := scrapeItem{
		Platform:     string(domain.PlatformInstagram),
		ExternalID:   p.Code,
		AuthorHandle: orDefault(p.User.Username, "unknown"),
		AuthorID:     anyToString(p.User.Pk),
		Text:         p.Caption.Text,
		MediaURLs:    media,
		Metrics:      metrics,
		// takenAt was computed on the next line and used ONLY for the inWindow
		// check, so every search-page post stored published_at NULL even though
		// the actor sent taken_at. The other six parsers all set it here; this
		// literal is the one call site missed when scrapeItem.PublishedAt was
		// introduced.
		PublishedAt: publishedAtPtr(takenAt),
	}
	return igSearchPost{item: item, takenAt: takenAt}, nil
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

// publishedAtPtr normalises the filter's takenAt into a nullable DB value. A
// zero time means the actor gave us no parseable date — we keep the row
// (inWindow treats a missing field as "not outside the window") but we must not
// invent a publish date for it, so the column stays NULL and the UI falls back
// to scrapedAt, labelled as such rather than passed off as a publish date.
func publishedAtPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	v := t.UTC()
	return &v
}

// parseActorTime reads a timestamp from an actor, accepting the layouts they
// actually emit and returning the zero time when none fit.
//
// The timezone-less case is not hypothetical: crawlerbros/instagram-keyword-
// search-scraper emits pub_date as "2026-08-03T07:06:51", which RFC3339 rejects
// (it requires an offset). That parse error was discarded with `_`, so every
// crawlerbros post had a zero taken_at — and inWindow KEEPS a zero timestamp, so
// the operator's date filter silently returned the whole result set instead of
// the window. Timestamps are UTC across these actors, so a missing offset is
// read as UTC rather than as "unknown".
func parseActorTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z0700",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.000Z",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// parseIGComments splits a post row into one scrapeItem per comment, and one
// per NESTED reply, depth-first so a parent always precedes its children (the
// ingestor resolves a child's parent by external id, so the parent row must
// exist first).
//
// Replies are nested inside their parent in the payload — never emitted as
// sibling top-level rows — so this recursion is the only thing that surfaces
// them. Without it every reply is dropped and the stored post looks
// reply-less. `parent` is the external id of the enclosing comment, empty for
// a top-level one, and becomes scrapeItem.ParentExternalID.
func parseIGComments(platform domain.Platform, post scrapeItem, comments []igCommentItem) []scrapeItem {
	items := make([]scrapeItem, 0, len(comments))
	var walk func(parent string, list []igCommentItem)
	walk = func(parent string, list []igCommentItem) {
		for _, c := range list {
			if c.ID == "" || strings.TrimSpace(c.Text) == "" {
				continue
			}
			items = append(items, scrapeItem{
				Platform:         string(platform),
				ExternalID:       c.ID,
				CommentOf:        post.ExternalID,
				ParentExternalID: parent,
				AuthorHandle:     orDefault(c.OwnerUsername, "unknown"),
				Text:             c.Text,
				Metrics:          map[string]any{"likes": c.LikesCount},
			})
			// Recurse: a reply can itself be replied to (IG nests more than one
			// level), and this comment becomes the parent for anything inside it.
			if len(c.Replies) > 0 {
				walk(c.ID, c.Replies)
			}
		}
	}
	walk("", comments)
	return items
}

// threadsRun carries one scrape run's post-id -> shortcode map. A Threads reply
// arrives as its own top-level dataset row and names its parent only by numeric
// `parent_post_id`, while post rows are keyed by shortcode; without this map a
// reply has no way to reach the post it belongs to and would be dropped.
//
// ponytail: an in-memory map, correct because the ingestor walks one run's
// payloads in `received_at` order (ListRawPayloadsByRun orders by it) and the
// actor emits a parent before its replies. It would not survive a caller that
// fetched items out of order or in parallel; if that ever happens, resolve
// through a persisted post_id column instead of this map.
type threadsRun struct {
	codes map[string]string
}

func newThreadsRun() *threadsRun {
	return &threadsRun{codes: make(map[string]string)}
}

// code remembers a post's shortcode so a later reply can name its parent.
func (t *threadsRun) code(postID, code string) {
	if t != nil && postID != "" && code != "" {
		t.codes[postID] = code
	}
}

// parent resolves a reply's `parent_post_id` to the parent's shortcode, or ""
// when the run never emitted that post — a reply whose parent fell outside the
// date window, which is dropped rather than rejected.
func (t *threadsRun) parent(postID string) string {
	if t == nil {
		return ""
	}
	return t.codes[postID]
}

// parseThreadsItem maps one themineworks/threads-scraper row into the canonical
// items it yields: a post, or a reply to one.
//
// This actor (a) replaced pro100chok/threads-scraper-usage and (b) does NOT nest
// replies under the post the way that one did. Each reply is its own top-level
// dataset row, flagged `is_reply` and naming its parent through
// `parent_post_id`, so a reply becomes a comment and is linked back through the
// run's post-id map. That is why the runner must send `includeReplies: true`:
// with the actor's default the reply rows never arrive and a Threads post
// ingests with zero comments.
//
// The wire names are raw Threads/snake_case (code / text / posted_at /
// like_count), matching the documented schema. The earlier actors' spellings
// (post_code / text_content / created_at / media_urls, and usage's taken_at /
// images / video_versions / replies[]) are kept as fallbacks: a row from either
// can still be sitting in object storage from an earlier run and is re-ingested
// on a forced scrape.
func parseThreadsItem(raw []byte, window port.KeywordTimeWindow, onlyCode string, run *threadsRun) ([]scrapeItem, error) {
	var src struct {
		// Current actor.
		PostID       string   `json:"post_id"`
		Code         string   `json:"code"`
		Text         string   `json:"text"`
		PostedAt     string   `json:"posted_at"`
		LikeCount    *int64   `json:"like_count"`
		ReplyCount   *int64   `json:"reply_count"`
		RepostCount  *int64   `json:"repost_count"`
		QuoteCount   *int64   `json:"quote_count"`
		MediaURLs    []string `json:"media_urls"`
		IsReply      bool     `json:"is_reply"`
		ParentPostID string   `json:"parent_post_id"`
		Username     string   `json:"username"`
		// Actor-level failures ("no_items", "not_found", ...) arrive as a row
		// carrying only this. Type is informational and never billed.
		Error string `json:"error"`
		Type  string `json:"_type"`

		// Legacy: pro100chok/threads-scraper-usage.
		TakenAt string   `json:"taken_at"`
		Images  []string `json:"images"`
		// Example: a video post carries its playable file here, not on `images`.
		VideoVersions []struct {
			URL string `json:"url"`
		} `json:"video_versions"`
		ReshareCt *int64 `json:"reshare_count"`

		// Legacy: futurizerush/meta-threads-scraper.
		PostCode    string `json:"post_code"`
		TextContent string `json:"text_content"`
		CreatedAt   string `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	if src.Error != "" {
		return nil, fmt.Errorf("actor error %q", src.Error)
	}
	// The run summary is informational and never billed; storing it as a post
	// would put an empty row on the dashboard for every scrape.
	if src.Type == "info" {
		return nil, nil
	}
	code := orDefault(src.Code, src.PostCode)
	if code == "" {
		return nil, errors.New("item has no post code")
	}
	text := orDefault(src.Text, src.TextContent)
	// media_items are not a flat list: this actor sends media_urls[], usage sent
	// images[] for an image post and video_versions[].url for a video one.
	// Copied, not aliased: an omitted media_urls leaves src.MediaURLs nil, and a
	// nil MediaURLs marshals to JSON null. The append below would also write
	// into the decoded slice's spare capacity.
	media := make([]string, 0, len(src.MediaURLs)+len(src.VideoVersions))
	media = append(media, src.MediaURLs...)
	if len(media) == 0 {
		media = src.Images
		for _, v := range src.VideoVersions {
			if strings.TrimSpace(v.URL) != "" {
				media = append(media, v.URL)
			}
		}
	}
	// A row with no parsable timestamp keeps a zero takenAt, which inWindow
	// treats as "keep" — a missing field is the actor's gap, not a date outside
	// the operator's window.
	takenAt := parseActorTime(orDefault(src.PostedAt, orDefault(src.TakenAt, src.CreatedAt)))
	if !inWindow(takenAt, window) {
		return nil, nil
	}
	handle := orDefault(src.Username, "unknown")

	// The reply branch: same row shape, linked to its parent through the run's
	// shortcode map. A single-post scrape (onlyCode set) is its own fallback:
	// the live actor omits `parent_post_id` there, yet every `is_reply` row in
	// that run can only belong to the post that was asked for. Without the
	// fallback a post scrape returned its replies as zero comments (verified
	// live: 24 dataset rows, all `is_reply: true`, all `parent_post_id: null`).
	if src.IsReply {
		parent := run.parent(src.ParentPostID)
		if parent == "" && onlyCode != "" {
			parent = onlyCode
		}
		if parent != "" && text != "" {
			return []scrapeItem{{
				Platform:     string(domain.PlatformThreads),
				ExternalID:   code,
				CommentOf:    parent,
				AuthorHandle: handle,
				Text:         text,
				Metrics:      map[string]any{"likes": src.LikeCount},
				PublishedAt:  publishedAtPtr(takenAt),
			}}, nil
		}
		// The parent is not in this run. The actor flags `is_reply` on rows
		// whose parent the scrape never delivered: its keyword mode marks a
		// reply without fetching the parent post, and `replied_to_post_code`
		// arrives empty alongside it. Dropping the row here lost 19 of 50
		// posts on a real batch. Store it as a top-level post instead — the
		// parent link is genuinely unavailable, but the post and its metrics
		// are not.
		//
		// ponytail: the stored post is then commentless and its `replied_to_*`
		// context is discarded. If the parent link matters later, resolve it
		// from `reply_to_username` + `replied_to_post_code` when the latter is
		// present.
	}

	run.code(src.PostID, code)
	// A permalink scrape accepts only the post it asked for: the actor's post
	// mode also returns share/quote context rows with no distinguishing field,
	// which would otherwise be stored as ordinary posts.
	if onlyCode != "" && onlyCode != code {
		return nil, nil
	}
	return []scrapeItem{{
		Platform:     string(domain.PlatformThreads),
		ExternalID:   code,
		AuthorHandle: handle,
		Text:         text,
		MediaURLs:    media,
		Metrics: map[string]any{
			"likes":    src.LikeCount,
			"comments": src.ReplyCount,
			"shares":   orInt(src.RepostCount, src.ReshareCt, src.QuoteCount),
		},
		PublishedAt: publishedAtPtr(takenAt),
	}}, nil
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
//
// The *int64 case exists because the Instagram and Threads wire structs use a
// pointer to tell "the actor reported 0" from "the actor reported nothing", and
// they put that pointer straight into scrapeItem.Metrics. Without this case
// numeric returned (0,false) for all of them, so the snapshot writer stored a
// real 0 for every IG and Threads post and ranked them all last in
// TopPostsByMetric. Nothing caught it because post.metrics is marshalled
// directly — json.Marshal follows the pointer — while the snapshot path goes
// through here. A nil pointer stays absent, which is the whole point of the
// pointer in the first place.
func numeric(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case *int64:
		if n == nil {
			return 0, false
		}
		return *n, true
	case string:
		// best-effort; a non-numeric string yields false
		var f float64
		if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
			return int64(f), true
		}
	}
	return 0, false
}
