package service

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// linkedInPost is one dataset row from harvestapi/linkedin-post-search
// (Apify id buIWk2uOUzTmcLsuB), the LinkedIn keyword-search actor.
//
// Nearly everything this actor sends is a nested object or an array, and Go's
// case-insensitive unmarshal cannot bridge a nesting level or an underscore —
// exactly the class of wire shape (TikTok's channel{}, Instagram's
// latestComments[]) that gets its own parser rather than a shared struct. The
// tags below are the keys verified on a live row (see
// testdata/linkedin_post_search_row.json); a renamed key here does not fail
// loudly, it silently produces an empty field, so every one the parser reads is
// pinned in scrape_linkedin_test.go.
type linkedInPost struct {
	// Type is "post" on a real post row. The actor also emits other types, which
	// are skipped rather than stored as posts (see parseLinkedInItems).
	Type string `json:"type"`
	// ID is the numeric activity id as a string, and is the same value as
	// EntityID. ID is preferred; EntityID is the fallback for a row that omits
	// it, and the two are never contradictory on a live row.
	ID       string           `json:"id"`
	EntityID string           `json:"entityId"`
	Content  string           `json:"content"`
	Author   linkedInAuthor   `json:"author"`
	PostedAt linkedInPostedAt `json:"postedAt"`
	// Engagement is read for the three counters only. Its reactions[] array is
	// the per-type breakdown of those same numbers (LIKE 15 + EMPATHY 1 = the 16
	// in likes) and is deliberately not parsed: it is not a second metric, and
	// storing it would need a key no other platform has.
	Engagement linkedInEngagement `json:"engagement"`
	PostImages []linkedInImage    `json:"postImages"`
	// Social carries the actor's visibility flags (hideReactionsCount, …).
	// Nothing is mapped from it: when LinkedIn hides a counter the actor reports
	// it as 0, and a hidden 0 is indistinguishable from a real 0 — so the honest
	// value is the number we were given, and the flag must not cost us the post.
	// It is declared anyway so the row is read as the object it is, and so the
	// flag names are documented in one place.
	Social linkedInSocial `json:"socialContent"`
	// Error carries an actor-level failure row ("no_items", "not_found", …)
	// instead of a post.
	Error string `json:"error"`
	// Deliberately absent, and each for a reason:
	//   - linkedinUrl / shareLinkedinUrl: there is no permalink column. The
	//     dashboard composes the post's URL from its id, as it does for the
	//     other platforms.
	//   - comments / commentIds: both are [] on a search row (this mode does not
	//     fetch comment bodies), and neither is ever a count — that is
	//     engagement.comments.
	//   - article / newsletterTitle / newsletterUrl: the attached article's
	//     metadata. It is context for the post's text, which we store anyway, and
	//     the media it may carry is on postImages.
	//   - contentAttributes / header / query / reactionIds / reactions: markup,
	//     run metadata or the redundant breakdown covered above.
}

// linkedInAuthor is the post's author. Both identifiers the ingestor wants are
// here: PublicIdentifier is the vanity handle and ID the member's opaque
// profile id.
//
// ID, Public and UniversalName are all `any` for one reason: a type mismatch on
// ANY key of the row fails the whole unmarshal, and this is the row whose loss
// costs a post's text, metrics and date. The ids are read through anyToString
// (already the codebase's tool for an id that arrives as number-or-string, as
// accessToken does), and a field nothing reads is never allowed to be the
// reason a post is dropped. A `*string` would take a numeric id down with it;
// a plain `string` would take a numeric universalName down with it.
type linkedInAuthor struct {
	ID     any `json:"id"`
	Name   any `json:"name"`
	Public any `json:"publicIdentifier"`
	// UniversalName is null on every row captured so far and is read by nothing.
	UniversalName any `json:"universalName"`
	LinkedInURL   any `json:"linkedinUrl"`
}

// linkedInPostedAt is the post's publish time, nested one level down. Date is
// the field to use (RFC3339 with a Z); Timestamp is the same instant in
// MILLISECONDS and is the fallback. The two postedAgo* fields are the actor's
// own human-readable rendering ("20h", "20 hours ago • Edited • …") and are
// deliberately not parsed: a relative string cannot be turned into a date
// without inventing one, and the absolute fields beside it are exact.
type linkedInPostedAt struct {
	Timestamp int64  `json:"timestamp"`
	Date      string `json:"date"`
}

// linkedInEngagement is the post's public engagement counters.
//
// Reactions is the type breakdown of the SAME likes number (LIKE 15 + EMPATHY 1
// = the 16 in Likes) and is not a separate metric — storing it would double the
// post's engagement and would need a key no other platform has. It is not
// parsed, by design.
type linkedInEngagement struct {
	Likes    *int64 `json:"likes"`
	Comments *int64 `json:"comments"`
	Shares   *int64 `json:"shares"`
}

// linkedInSocial carries the actor's visibility flags, e.g. hideReactionsCount.
// Nothing here is mapped: when LinkedIn hides a counter the actor reports it as
// 0, and a hidden zero is indistinguishable from a real zero — so the honest
// representation is the number we were given, not a guess at which zeros are
// hidden. It is declared so a row carrying flags (rather than failing to
// unmarshal) is parsed normally; hiding a count is not an error and must not
// cost us the post.
type linkedInSocial struct {
	HideCommentsCount  bool `json:"hideCommentsCount"`
	HideReactionsCount bool `json:"hideReactionsCount"`
	HideRepostsCount   bool `json:"hideRepostsCount"`
	HideViewsCount     bool `json:"hideViewsCount"`
}

// linkedInImage is one element of postImages[].
//
// NOT verified against a live row: the one captured row has postImages: [] and
// no image-bearing row was available, so this shape follows the actor's
// documented media object (url/width/height, the same keys its author.avatar
// object uses). linkedInImageURL therefore accepts the spellings a media object
// plausibly carries and falls back to a recursive hunt for the first URL
// anywhere in the entry — so a differently-keyed image still resolves instead
// of silently becoming "this post has no media". A test pins each path.
//
// Every field is `any`, not `string`: a guessed key that arrives with a
// different type (image: {…} rather than image: "…") would otherwise fail the
// WHOLE row — losing the post, its text, its metrics and its date — over a
// field nothing here even reads.
type linkedInImage map[string]any

// linkedInImageURL pulls an image URL out of one postImages[] entry.
func linkedInImageURL(img linkedInImage) string {
	// Known spellings first, in the order a media object is likely to mean them
	// (the full-size url before a thumbnail-ish sibling).
	for _, key := range []string{"url", "src", "imageUrl", "image", "mediaUrl", "fullImage"} {
		if s, ok := img[key].(string); ok && s != "" {
			return s
		}
	}
	// Last resort: the entry nests the URL one level deeper under a name this
	// build did not anticipate. Any string value that reads as a URL is one, and
	// the walk is type-tolerant so an unexpected shape cannot fail the row.
	return firstURLValue(map[string]any(img), 0)
}

// firstURLValue walks an arbitrary JSON value looking for the first string that
// reads as a URL. depth bounds the walk: media objects are shallow, and a
// self-referencing payload must not be able to spin here.
func firstURLValue(v any, depth int) string {
	if depth > 4 {
		return ""
	}
	switch t := v.(type) {
	case string:
		if looksLikeURL(t) {
			return t
		}
	case map[string]any:
		for _, child := range t {
			if u := firstURLValue(child, depth+1); u != "" {
				return u
			}
		}
	case []any:
		for _, child := range t {
			if u := firstURLValue(child, depth+1); u != "" {
				return u
			}
		}
	}
	return ""
}

// looksLikeURL is deliberately loose — a URL is anything with an http(s)
// scheme — because it only has to be stricter than "any non-empty string",
// which would happily pick up an image's caption or alt text as its URL.
func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// parseLinkedInItems maps one harvestapi/linkedin-post-search dataset row.
//
// The window is applied here because this actor has no date input at all: its
// own `query` object is the search it ran (sortBy/page/search/postedLimit), and
// nothing in the payload bounds a date. So the ingestor's window is the only
// filter between what we paid for and what we store — see
// docs/platforms/linkedin.md.
//
// A row carrying neither id nor entityId is malformed and errors (the caller
// skips it with a warning); a row of any other `type` yields no items, so the
// actor's non-post rows never land in the post table.
func parseLinkedInItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
	var src linkedInPost
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	if src.Error != "" {
		return nil, errors.New("linkedin actor error: " + src.Error)
	}
	// A non-post type is a row the actor produced for something other than the
	// post we asked for. Dropped silently (like the Threads run summary), not
	// errored: it is not a broken row, it is a row we do not want.
	if src.Type != "" && src.Type != "post" {
		return nil, nil
	}
	id := orDefault(src.ID, src.EntityID)
	if id == "" {
		return nil, errors.New("linkedin item has no id")
	}
	publishedAt := linkedInPublishedAt(src.PostedAt)
	// A zero date is kept, not dropped — a missing field is the actor's gap,
	// not a date outside the operator's window (inWindow says the same).
	if !inWindow(publishedAt, window) {
		return nil, nil
	}
	// Non-nil even when empty: media_urls is stored and served as a JSON array,
	// where null and [] are different things to every downstream reader.
	media := make([]string, 0, len(src.PostImages))
	for _, img := range src.PostImages {
		if u := linkedInImageURL(img); u != "" {
			media = append(media, u)
		}
	}
	metrics := map[string]any{}
	// Pointer-presence, not non-zero: a post with 0 comments has a comment count
	// of zero, and the actor does report that. Only a field the row omitted is
	// left out.
	if src.Engagement.Likes != nil {
		metrics["likes"] = *src.Engagement.Likes
	}
	if src.Engagement.Comments != nil {
		metrics["comments"] = *src.Engagement.Comments
	}
	if src.Engagement.Shares != nil {
		metrics["shares"] = *src.Engagement.Shares
	}
	// No `views` key on any code path. LinkedIn publishes no view count, and a
	// `views: 0` here would be read by the snapshot writer as a real measurement
	// — every LinkedIn post would rank last behind posts that genuinely have no
	// views. Absent is the honest answer, and metricsInt already treats it as 0.
	return []scrapeItem{{
		Platform: string(domain.PlatformLinkedIn),
		// The numeric activity id, which is what the actor's own linkedinUrl
		// carries (…-activity-<id>-XgqS) and what a re-scrape will match.
		ExternalID: id,
		// publicIdentifier is the vanity handle ("miketemkin"); the opaque
		// member id in author.id is the fallback for a company page or any row
		// the actor sends without a public identifier.
		AuthorHandle: orDefault(anyToString(src.Author.Public), orDefault(anyToString(src.Author.ID), "unknown")),
		AuthorID:     orDefault(anyToString(src.Author.ID), id),
		Text:         src.Content,
		MediaURLs:    media,
		Metrics:      metrics,
		PublishedAt:  publishedAtPtr(publishedAt),
	}}, nil
}

// linkedInPublishedAt reads postedAt.date (RFC3339, Z-suffixed) and falls back
// to postedAt.timestamp, which is MILLISECONDS — not the Unix seconds the other
// actors' flat fields use, so a parser that read it with time.Unix would date
// every LinkedIn post to 1970 and then silently drop it as out-of-window.
func linkedInPublishedAt(p linkedInPostedAt) time.Time {
	if t := parseActorTime(p.Date); !t.IsZero() {
		return t
	}
	if p.Timestamp > 0 {
		return time.UnixMilli(p.Timestamp).UTC()
	}
	return time.Time{}
}
