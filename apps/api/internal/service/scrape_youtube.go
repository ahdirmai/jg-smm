package service

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// youTubeVideo is one dataset row from apidojo/youtube-scraper-api (Apify id
// gXSReGYeawn5nwDhI), the YouTube keyword-search actor.
//
// The author and the media are each a nested shape, which is why this platform
// needs its own parser rather than the loose self-describing struct: Go's
// case-insensitive unmarshal cannot bridge a nesting level, and while `channel`
// would quietly yield an empty handle, `thumbnails` is an ARRAY of objects with
// a url inside each — reading it into scrapeItem.MediaURLs directly is not a
// near miss, it is a type error that fails the whole row, one paid-for post at a
// time. The keys below were verified against live rows (see
// testdata/youtube_rows.json); a renamed key here does not fail loudly, it
// silently produces an empty field, so every one the parser reads is pinned in
// scrape_youtube_test.go.
type youTubeVideo struct {
	// ID is the YouTube video id ("k_ldBC5mQu8") — the `v` parameter of the
	// permalink and the stem of every thumbnail path. It is NOT a URN
	// ("yt:video:…") and it is not the channel id.
	ID string `json:"id"`
	// Title is the video's headline and is what we store as the post text. The
	// row's `description` is the full body copy (and is truncated by the actor
	// at a couple of paragraphs), but the brief for this platform is explicit:
	// title is the post text. `description` is deliberately not read — it is a
	// second, longer string that the ingestor has nowhere to put, and folding it
	// into Text would write something the operator never saw as the headline.
	Title string `json:"title"`
	// URL is the permalink. The ingestor has no column for it (see
	// youTubePermalink), so it is read only to pin the precedence in one place.
	// EmbedURL points at /embed/<id>, the player form of the same video. It is
	// declared for the same reason as URL — to pin the permalink precedence —
	// and is used only by youTubePermalink.

	// UploadDate is the video's publish instant, ISO WITH a timezone offset
	// ("2026-09-13T01:25:08-07:00"). PublishDate and RelativeDate carry the SAME
	// instant on every captured row — despite the name, relativeDate is not a
	// rendered "3 days ago" string on this actor — so they are ordered fallbacks
	// rather than peers, and the first one that parses wins.
	UploadDate   string `json:"uploadDate"`
	PublishDate  string `json:"publishDate"`
	RelativeDate string `json:"relativeDate"`

	// Views/Likes/Comments are flat top-level COUNTS, present on every captured
	// row. YouTube publishes no share and no bookmark count for a video and this
	// actor does not invent one, so no `shares` or `collects` key is ever
	// written — see parseYouTubeItems.
	Views    *int64 `json:"views"`
	Likes    *int64 `json:"likes"`
	Comments *int64 `json:"comments"`

	// Channel is the video's author. Handle already carries a leading "@"
	// ("@K7_Bakujuk"), matching what the sibling parsers store after their own
	// normalisation, and ID is the opaque channel id ("UC…").
	Channel struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Handle string `json:"handle"`
		URL    string `json:"url"`
		// SubscriberCount is a STRING with the unit baked in ("21.2K
		// subscribers"), not a number. There is no canonical metric key for a
		// subscriber count on a POST, and parsing "21.2K" into 21200 would store
		// a rounded approximation as if it were exact — so it is declared here
		// only so the field is visibly accounted for, and never mapped.
		SubscriberCount string `json:"subscriberCount"`
	} `json:"channel"`

	// Thumbnails is an ARRAY of size variants, from ~168x94 up to 1920x1080. See
	// youTubeLargestThumbnail: the largest by pixel area is picked, not the
	// first, and an empty array is handled.
	Thumbnails []youTubeThumbnail `json:"thumbnails"`

	// Status is "OK" on a healthy row. Error carries an actor-level failure row
	// ("no_items", …) instead of a video.
	Status string `json:"status"`
	Error  string `json:"error"`

	// `streamingData` and `captions` are deliberately NOT declared. Both are
	// large nested blobs whose only URLs are short-lived signed ones that expire
	// within hours (streamingData.expiresInSeconds was 21540 on the captured
	// row), so anything stored from them is a dead link by the time the
	// dashboard reads it. Leaving them out of the struct means they cannot be
	// reached by accident.
}

// youTubeThumbnail is one element of thumbnails[]. Width and Height are numbers
// on every captured row and are what the largest-variant choice is made on.
type youTubeThumbnail struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// parseYouTubeItems maps one apidojo/youtube-scraper-api dataset row.
//
// The window is applied here for the same reason as the TikTok, X, Facebook and
// LinkedIn parsers: the ingestor's window is the backstop between what we paid
// for and what we store. This actor is unusual in that it DOES accept a
// server-side uploadDate filter ("any"/"l"/"t"/"w"/"m"/"y"), so the operator's
// window is pushed down to it in the runner payload — but that filter is
// relative and coarse ("this month"), which is not the same predicate as an
// absolute from/to, so it narrows the pool rather than replacing this check.
// See docs/platforms/youtube.md.
//
// A row carrying an actor error is malformed for our purposes and errors (the
// caller skips it with a warning) rather than being stored as a post keyed by an
// error message. A row with no video id is the same kind of malformed.
func parseYouTubeItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
	var src youTubeVideo
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	if src.Error != "" {
		return nil, errors.New("youtube actor error: " + src.Error)
	}
	// A zero Status means the field was absent, which is not a failure flag; any
	// other non-OK status is the actor telling us this row did not resolve to a
	// playable video, and it must not be stored as one.
	if src.Status != "" && src.Status != "OK" {
		return nil, nil
	}
	if src.ID == "" {
		return nil, errors.New("youtube item has no id")
	}

	publishedAt := youTubePublishedAt(src)
	// A zero date is KEPT, not dropped — a missing field is the actor's gap,
	// not a date outside the operator's window (inWindow says the same).
	if !inWindow(publishedAt, window) {
		return nil, nil
	}

	// Non-nil even when empty: media_urls is stored and served as a JSON array,
	// where null and [] are different things to every downstream reader. A post
	// whose row carries no thumbnails is stored with no media, not dropped.
	media := make([]string, 0, 1)
	if u := youTubeLargestThumbnail(src.Thumbnails); u != "" {
		media = append(media, u)
	}

	metrics := map[string]any{}
	// Pointer-presence, not non-zero: a video with 0 comments genuinely has a
	// zero comment count, and the actor does report that. Only a field the row
	// omitted is left out. The three keys are the canonical ones the snapshot
	// writer reads (views/likes/comments), so no alias is needed.
	if src.Views != nil {
		metrics["views"] = *src.Views
	}
	if src.Likes != nil {
		metrics["likes"] = *src.Likes
	}
	if src.Comments != nil {
		metrics["comments"] = *src.Comments
	}
	// No `shares` and no `collects` key on any code path. YouTube has no share
	// or save count on a video, this actor sends neither, and a stored zero
	// would be read by the snapshot writer as a real measurement — every YouTube
	// post ranking last on shares behind posts that genuinely have none. Absent
	// is the honest answer, and metricsInt already treats it as 0.

	return []scrapeItem{{
		Platform: string(domain.PlatformYouTube),
		// The video id, which is the `v` parameter of the permalink and what a
		// re-scrape will match.
		ExternalID: src.ID,
		// channel.handle, which is stable and already @-prefixed (the sibling
		// parsers store the same thing; Instagram's username, TikTok's
		// channel.username). The display name is the fallback for a row that
		// omits the handle: a channel's name is a usable handle for display,
		// unlike the round trip through channel.url a numeric-id URL would need.
		AuthorHandle: orDefault(strings.TrimSpace(src.Channel.Handle), orDefault(strings.TrimSpace(src.Channel.Name), "unknown")),
		AuthorID:     orDefault(src.Channel.ID, src.ID),
		Text:         src.Title,
		MediaURLs:    media,
		Metrics:      metrics,
		PublishedAt:  publishedAtPtr(publishedAt),
	}}, nil
}

// youTubePublishedAt reads the row's publish instant, trying uploadDate first
// and falling back to publishDate and then relativeDate.
//
// All three carry the SAME ISO-with-offset instant on every captured row, so
// this ordering is only observable when a build omits one — which is exactly
// the kind of gap that otherwise costs the post either its date or, worse, the
// post itself: a row whose uploadDate alone were missing would be stored
// undated even though its date sits in the field right beside it.
//
// parseActorTime already accepts RFC3339 and therefore the "-07:00" offset
// (time.RFC3339 handles a numeric offset, not only "Z"), plus the offsetless
// layouts as a safety net. Its result is UTC, so the offset is resolved rather
// than discarded — the captured row's 01:25:08-07:00 is 08:25:08Z.
func youTubePublishedAt(src youTubeVideo) time.Time {
	for _, s := range []string{src.UploadDate, src.PublishDate, src.RelativeDate} {
		// non-nil and non-zero, not a truthiness shortcut: these are strings.
		if strings.TrimSpace(s) == "" {
			continue
		}
		if t := parseActorTime(s); !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// youTubeLargestThumbnail picks the URL of the largest variant in thumbnails[]
// by pixel area, or the empty string when there is nothing usable.
//
// By AREA, not by index and not by width alone. The array is ordered
// smallest-first on every row captured so far, so `thumbnails[0]` happens to
// return the 168x94 speck and `thumbnails[len-1]` the 1280x720 image — but an
// actor is free to reorder its own array, and an ordering assumption that is
// right today is a wrong thumbnail tomorrow with nothing failing. Area also
// beats width, because a 1920x1080 video frame and a 1080x1920 Short do not
// compare by one dimension.
//
// The captured rows carry a 1920x1080 .webp ALONGSIDE a 1280x720 .jpg, and the
// webp wins on area (2,073,600 vs 921,600). That is intended: the ask is the
// largest by pixel area, and .webp is rendered by every current browser. An
// entry with no URL is skipped rather than stored as a broken image, and an
// empty (or nil) array yields "" — the caller stores the post with no media
// instead of dropping a paid-for row over a missing optional array.
func youTubeLargestThumbnail(thumbs []youTubeThumbnail) string {
	best := ""
	bestArea := 0
	for _, t := range thumbs {
		u := strings.TrimSpace(t.URL)
		if u == "" {
			continue
		}
		area := t.Width * t.Height
		// `>=` would make the LAST entry win among equal-area ties; `>` keeps
		// the first, so a payload carrying the same variant twice is stable.
		if best == "" || area > bestArea {
			best, bestArea = u, area
		}
	}
	return best
}
