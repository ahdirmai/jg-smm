package service

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// facebookPost is one dataset row from danek/facebook-search-ppr (Apify id
// l6CUZt8H0214D3I0N), the Facebook keyword-search actor.
//
// The author and the media are each a nested object, which is why this platform
// needs its own parser: Go's case-insensitive unmarshal cannot bridge a nesting
// level, exactly the class of wire shape (TikTok's channel{}, LinkedIn's
// author{}, Instagram's latestComments[]) that gets its own struct. The keys
// below were verified against a live row (see
// testdata/facebook_row.json); a renamed key here does not fail loudly, it
// silently produces an empty field — so every one the parser reads is pinned in
// scrape_facebook_test.go.
type facebookPost struct {
	// PostID is the actor's canonical post id and the key the post is stored
	// under. It is a STRING ("1501360315021356"), not a number — reading it as
	// an int64 loses ids past 2^53, and a float64 unmarshal rounds some of them.
	PostID string `json:"post_id"`
	// Type is "post" on a real post row. Informational, and never billed.
	Type string `json:"type"`
	// URL is the permalink. It can point at a group post (/groups/<g>/posts/
	// <id>/), a page post, or a video (/watch/?v=<id> or /videos/<id>). It is
	// NOT the external id — post_id is.
	URL string `json:"url"`
	// Message is the post text. It is null/empty for a photo- or link-only
	// post; MessageRich is the same text with the actor's own soft line breaks
	// preserved, so it is the fallback rather than an upgrade.
	Message     string `json:"message"`
	MessageRich string `json:"message_rich"`
	// Timestamp is Unix SECONDS as a number (NOT milliseconds — LinkedIn's
	// postedAt.timestamp is ms, and copying that conversion here would date
	// every Facebook post to 1970 and then drop it as out-of-window).
	Timestamp int64 `json:"timestamp"`
	// Facebook calls them reactions, not likes. The snapshot writer reads the
	// canonical "likes" key, so reactions_count is mapped to it below.
	ReactionsCount *int64 `json:"reactions_count"`
	CommentsCount  *int64 `json:"comments_count"`
	ReshareCount   *int64 `json:"reshare_count"`
	// Author is a NESTED object, the same shape as authors[] (whose first
	// element is the post's author). author_title is null on the captured rows.
	Author struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"author"`
	AuthorTitle string `json:"author_title"`
	// Image is a nested object or null. uri is the CDN URL, which carries a long
	// signed query string — it is still just a URL and is kept whole.
	Image *struct {
		URI string `json:"uri"`
	} `json:"image"`
	// A video post carries its playable file under video_files[] and its frame
	// under video_thumbnail.uri; both are null on a photo post. Facebook exposes
	// NO view count for a search result — there is no `video_view_count` on the
	// captured rows and no `views` key at all.
	Video *struct {
		URI string `json:"uri"`
	} `json:"video"`
	VideoFiles []struct {
		URI string `json:"uri"`
	} `json:"video_files"`
	VideoThumbnail *struct {
		URI string `json:"uri"`
	} `json:"video_thumbnail"`
	// An album post carries its pictures in album_preview[], an array of
	// {type, image_file_uri} — measured 2026-09-29 on a live run, where a post
	// with an empty `image` object still had its picture sitting here. Without
	// this the post stored with zero media, which is exactly the TikTok bug
	// class: a post that renders as a bare text row for everyone downstream.
	AlbumPreview []struct {
		Type         string `json:"type"`
		ImageFileURI string `json:"image_file_uri"`
	} `json:"album_preview"`
	// ExternalURL is the link out of a link post, non-null only there.
	ExternalURL *string `json:"external_url"`
	// Error carries an actor-level failure row ("no_items", "not_found", …)
	// instead of a post.
	Error string `json:"error"`
}

// parseFacebookItems maps one danek/facebook-search-ppr dataset row.
//
// The window is applied here because this actor has no date input: its input is
// `query`, `search_type` and `max_posts` only, with no since/until equivalent.
// So the ingestor's window is the only filter between what we paid for and what
// we store — the same situation as the TikTok and crawlerbros parsers, and the
// same reason the filter lives here and not in the runner payload.
//
// A row carrying an actor error is malformed for our purposes and errors (the
// caller skips it with a warning) rather than being stored as a post keyed by
// an error message. A row of any non-post `type` yields no items.
func parseFacebookItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
	var src facebookPost
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	if src.Error != "" {
		return nil, errors.New("facebook actor error: " + src.Error)
	}
	// `type` is the actor's own label for the row. "post" is the common case, a
	// video post is "video" (still a post we want), and a row the actor emitted
	// for something else — a comment, a page header — carries that other value.
	// Only those are dropped, silently, like the Threads run summary: not a
	// broken row, just one we do not want. An EMPTY type is kept, because the
	// field being absent is the actor's gap, not evidence of a non-post row.
	if src.Type != "" && src.Type != "post" && src.Type != "video" {
		return nil, nil
	}
	id := src.PostID
	if id == "" {
		// Fall back to the permalink before giving up: in case a build omits
		// post_id on a video row. Still no id ⇒ genuinely malformed, and saying
		// so is kinder than storing a post under an empty key.
		id = facebookIDFromURL(src.URL)
	}
	if id == "" {
		return nil, errors.New("facebook item has no post_id")
	}

	// The actor's timestamp is Unix seconds. A zero/missing timestamp keeps a
	// zero time, which inWindow treats as "keep" (the actor's gap, not a date
	// outside the operator's window), and PublishedAt is then nil rather than a
	// fabricated 1970.
	publishedAt := time.Time{}
	if src.Timestamp != 0 {
		publishedAt = time.Unix(src.Timestamp, 0).UTC()
	}
	if !inWindow(publishedAt, window) {
		return nil, nil
	}

	// A photo post's text is `message`; `message_rich` differs only in the soft
	// line breaks the actor inserts. Falling back to it keeps a post whose text
	// lives only there from being stored empty, which the UI renders as a bare
	// image with no caption at all.
	text := orDefault(src.Message, src.MessageRich)

	// Non-nil even when empty: media_urls is stored and served as a JSON array,
	// where null and [] are different things to every downstream reader.
	media := make([]string, 0, 2)
	if len(src.VideoFiles) > 0 {
		// A video post: the playable file is the media. The thumbnail is only
		// the frame, so it is the fallback, not a peer.
		for _, f := range src.VideoFiles {
			if strings.TrimSpace(f.URI) != "" {
				media = append(media, f.URI)
			}
		}
	} else if src.VideoThumbnail != nil && strings.TrimSpace(src.VideoThumbnail.URI) != "" {
		media = append(media, src.VideoThumbnail.URI)
	}
	// A photo post carries its image here. On every captured row exactly one of
	// the video/image branches is populated, but they are not mutually exclusive
	// in the wire shape, so both may contribute.
	if src.Image != nil && strings.TrimSpace(src.Image.URI) != "" {
		media = append(media, src.Image.URI)
	}
	for _, a := range src.AlbumPreview {
		if strings.TrimSpace(a.ImageFileURI) != "" {
			media = append(media, a.ImageFileURI)
		}
	}
	if len(media) == 0 && src.ExternalURL != nil && strings.TrimSpace(*src.ExternalURL) != "" {
		// A link post has no image of its own; its external_url is the only
		// thing on the row that renders as anything. Store it so the post is not
		// thumbnail-less.
		media = append(media, *src.ExternalURL)
	}

	metrics := map[string]any{}
	// Pointer-presence, not non-zero: a post with 0 comments has a comment count
	// of zero and the actor does report that. Only a field the row omitted is
	// left out. reactions_count maps to "likes" — the canonical key the snapshot
	// writer reads; the platform's own name is kept alongside it.
	if src.ReactionsCount != nil {
		metrics["likes"] = *src.ReactionsCount
		// NOT also under "reactions": the snapshot writer has no such key, so a
		// second copy is a value stored and read by nobody.
	}
	if src.CommentsCount != nil {
		metrics["comments"] = *src.CommentsCount
	}
	if src.ReshareCount != nil {
		metrics["shares"] = *src.ReshareCount
	}
	// No `views` key on any code path. Facebook publishes no view count, and a
	// `views: 0` would be read by the snapshot writer as a real measurement —
	// every Facebook post would rank last behind posts that genuinely have no
	// views. Absent is the honest answer, and metricsInt already treats it as 0.

	return []scrapeItem{{
		Platform: string(domain.PlatformFacebook),
		// The actor's own post_id, which is what the permalink carries and what
		// a re-scrape will match.
		ExternalID: id,
		// The handle is the last path segment of author.url ("rudi.andika.182"),
		// not the display name ("Rudi") — matching what the sibling parsers store
		// (Instagram's username, TikTok's channel.username, Threads' username).
		AuthorHandle: facebookHandle(src.Author.URL, src.Author.Name),
		AuthorID:     orDefault(src.Author.ID, id),
		Text:         text,
		MediaURLs:    media,
		Metrics:      metrics,
		PublishedAt:  publishedAtPtr(publishedAt),
	}}, nil
}

// facebookHandle derives the author's handle from author.url, matching the
// sibling parsers' handles: a stable @-less segment, not the display name.
// A handle identical to the author id tells a reader nothing, so a pfbid is
// never accepted as a handle. Facebook renders an author's permalink in at
// least three shapes — https://www.facebook.com/<handle>,
// /people/<Display>/pfbid…, and a bare https://www.facebook.com/pfbid… — and
// the last two both bottom out in a pfbid. Measured 2026-09-29: every group
// post stored a pfbid as BOTH author_handle and author_id, i.e. the handle
// column was a copy of the id column. Keying on the "pfbid" prefix rather than
// on the URL shape is what makes this hold for all three forms.
//
// The display name is the honest fallback: it is what a reader can act on. A
// row with neither a usable permalink nor a name keeps "unknown", and the
// ingestor substitutes that for an empty value anyway.
func facebookHandle(rawURL, name string) string {
	display := strings.TrimSpace(name)
	if u := strings.TrimSpace(rawURL); u != "" {
		if parsed, err := url.Parse(u); err == nil {
			seg := strings.Trim(parsed.Path, "/")
			if i := strings.LastIndex(seg, "/"); i >= 0 {
				seg = seg[i+1:]
			}
			// A Page's permalink ends in its numeric member id
			// (/people/Info-Batulicin-Simpang-Empat/61573191947639/), so an
			// all-digits segment is an opaque id too, not a vanity handle.
			if seg != "" && !isFacebookIDSegment(seg) {
				return seg
			}
			// A pfbid segment: the Page's display name sits just before it, as in
			// /people/Muhammad-Saidi/pfbid02…, so take that instead.
			head := strings.Trim(parsed.Path, "/")
			if i := strings.LastIndex(head, "/"); i >= 0 {
				head = head[:i]
			}
			if j := strings.LastIndex(head, "/"); j >= 0 {
				head = head[j+1:]
			}
			if head != "" && !strings.EqualFold(head, "people") && !isFacebookIDSegment(head) {
				return head
			}
		}
	}
	return orDefault(display, "unknown")
}

// isFacebookIDSegment reports whether a URL path segment is an opaque Facebook
// identifier rather than a human-usable handle: either a "pfbid…" token or an
// all-digits member id. Both shapes were measured on live rows — the pfbid on
// /people/Endhiet-Chodetz/pfbid02… and the digits on
// /people/Info-Batulicin-Simpang-Empat/61573191947639/. Keying on the segment's
// own shape rather than on one URL spelling is what covers them all.
func isFacebookIDSegment(s string) bool {
	return strings.HasPrefix(strings.ToLower(s), "pfbid") || allDigits(s)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// facebookIDFromURL pulls a post id out of a permalink as a last resort. Group
// and page posts put it after /posts/; a video puts it after /videos/ or in the
// `v` query parameter of /watch/. An id Facebook renders as "pfbid…" is kept as
// is — it is what the actor's own post_id would be.
func facebookIDFromURL(rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return ""
	}
	trimmed := u
	if i := strings.IndexAny(trimmed, "?#"); i >= 0 {
		trimmed = trimmed[:i]
	}
	segs := strings.Split(strings.Trim(trimmed, "/"), "/")
	for i := 0; i+1 < len(segs); i++ {
		switch segs[i] {
		case "posts", "videos":
			if segs[i+1] != "" {
				return segs[i+1]
			}
		}
	}
	// /watch/?v=<id>: the id is in the query, not the path.
	if strings.Contains(u, "/watch") {
		if parsed, err := url.Parse(u); err == nil {
			if v := parsed.Query().Get("v"); v != "" {
				return v
			}
		}
	}
	return ""
}
