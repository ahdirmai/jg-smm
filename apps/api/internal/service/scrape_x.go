package service

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// xTimeLayout is Twitter's legacy created_at format, e.g.
// "Sun Sep 27 13:23:06 +0000 2026".
//
// It is NOT parseable by parseActorTime: that helper's layouts are all
// ISO-8601 or SQL-date shapes, and none of them accepts a weekday, a
// month *name*, a space-padded day, or a zone-before-year order. It is NOT
// parseable by time.RFC3339 either, which requires a "T" separator and an
// offset. Handing createdAt to parseActorTime returns the zero time, and
// inWindow KEEPS a zero time — so every X post would sail through the date
// filter and be stored with published_at NULL while looking exactly like the
// other platforms' posts. Hence a dedicated layout.
//
// The layout's parts, in Twitter's order (the Go reference time is
// Mon Jan 2 15:04:05 -0700 2006):
//
//	Mon   — weekday name
//	Jan   — month name
//	2     — space-padded day of month (" 7" for the 7th; the layout's "2"
//	        pads with a space where "02" would demand a zero)
//	15:04:05
//	-0700 — numeric zone, which Twitter's +0000 matches
//	2006  — year LAST, after the zone
var xTimeLayout = "Mon Jan 2 15:04:05 -0700 2006"

// xTweet is one dataset row from apidojo/twitter-scraper-lite: the native
// Twitter API v2 tweet object, so its metrics sit flat on the row and only
// the author is nested.
type xTweet struct {
	ID string `json:"id"`
	// Text is the post body; FullText held the identical value on every
	// captured row and is the fallback for a row that carries only one.
	Text     string `json:"text"`
	FullText string `json:"fullText"`
	// URL is the permalink on the x.com domain; TwitterURL is the same post
	// rehosted under twitter.com. URL is preferred, TwitterURL is the
	// fallback, and a row carrying neither falls back to the derived form.
	// CreatedAt is Twitter's legacy date format, parsed below with its own
	// layout — see xTimeLayout.
	CreatedAt string `json:"createdAt"`
	// Pointers, not plain ints, for the same reason the YouTube, LinkedIn and
	// Facebook parsers use them: a tweet with genuinely 0 likes is a real
	// measurement and must be stored, while an absent field is the actor's gap
	// and must stay absent. A plain int cannot tell the two apart, so a
	// `!= 0` guard silently dropped every real zero — which made the same
	// logical state serialise differently on X than on the other six.
	LikeCount    *int64 `json:"likeCount"`
	RetweetCount *int64 `json:"retweetCount"`
	ReplyCount   *int64 `json:"replyCount"`
	ViewCount    *int64 `json:"viewCount"`
	// Media and Videos are separate arrays; both are empty on a text-only
	// tweet and both populate on a media tweet. Either may be absent.
	Media []struct {
		// Twitter sends the media URL under `url` and the still frame under
		// `previewImageUrl`/`previewImage`. Only real, non-empty strings are
		// used; a media object with none of them contributes nothing.
		URL             string `json:"url"`
		MediaURL        string `json:"media_url"`
		MediaURLHTTPS   string `json:"media_url_https"`
		PreviewImageURL string `json:"previewImageUrl"`
		PreviewImage    string `json:"previewImage"`
		Type            string `json:"type"`
	} `json:"media"`
	Videos []struct {
		URL             string `json:"url"`
		PreviewImageURL string `json:"previewImageUrl"`
		PreviewImage    string `json:"previewImage"`
	} `json:"videos"`
	// Author is the one nested object. UserName is the handle WITHOUT a
	// leading "@" — the same convention the stored author_handle column uses
	// for every platform (TikTok's channel.username carries no "@" either;
	// if this parser prepended one, the X row would render as "@@kntngsy").
	Author struct {
		ID       string `json:"id"`
		UserName string `json:"userName"`
	} `json:"author"`
}

// parseXItems maps one apidojo/twitter-scraper-lite dataset row into a
// scrapeItem.
//
// The window is applied here and nowhere else: the search actor takes a
// keyword and a cap, and its date field is not what the operator's window
// means, so nothing upstream can narrow the pool for us. A post whose
// createdAt cannot be read is KEPT with published_at NULL (a missing date is
// the actor's gap, not a date outside the window) — the same rule as the other
// parsers.
//
// quoteCount and bookmarkCount are deliberately dropped. There is no canonical
// key for a repost-of-a-repost or a save count, and inventing `quotes` or
// `bookmarks` would store a value that the snapshot writer — which reads
// views/likes/comments/shares — never reads, i.e. a column that looks
// populated and is read by nobody.
func parseXItems(raw []byte, window port.KeywordTimeWindow) ([]scrapeItem, error) {
	var src xTweet
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	if src.ID == "" {
		return nil, errors.New("x item has no id")
	}

	publishedAt := parseActorTime(src.CreatedAt)
	if publishedAt.IsZero() {
		publishedAt = parseXTime(src.CreatedAt)
	}
	if !inWindow(publishedAt, window) {
		return nil, nil
	}

	// Non-nil even when empty: this list is stored and served as a JSON array,
	// and a null there is a different thing to every reader downstream.
	media := make([]string, 0, len(src.Media)+len(src.Videos))
	for _, m := range src.Media {
		if u := firstNonEmpty(m.URL, m.MediaURLHTTPS, m.MediaURL, m.PreviewImageURL, m.PreviewImage); u != "" {
			media = append(media, u)
		}
	}
	for _, v := range src.Videos {
		if u := firstNonEmpty(v.URL, v.PreviewImageURL, v.PreviewImage); u != "" {
			media = append(media, u)
		}
	}

	metrics := map[string]any{}
	if src.LikeCount != nil {
		metrics["likes"] = *src.LikeCount
	}
	// retweetCount is the share count; `shares` is also a key metricsInt reads
	// directly, unlike a `retweets` spelling which only this parser would know.
	if src.RetweetCount != nil {
		metrics["shares"] = *src.RetweetCount
	}
	if src.ReplyCount != nil {
		metrics["comments"] = *src.ReplyCount
	}
	if src.ViewCount != nil {
		metrics["views"] = *src.ViewCount
	}

	author := strings.TrimPrefix(strings.TrimSpace(src.Author.UserName), "@")
	return []scrapeItem{{
		Platform:     string(domain.PlatformX),
		ExternalID:   src.ID,
		AuthorHandle: orDefault(author, "unknown"),
		AuthorID:     src.Author.ID,
		Text:         xTweetText(src),
		MediaURLs:    media,
		Metrics:      metrics,
		PublishedAt:  publishedAtPtr(publishedAt),
	}}, nil
}

// xTweetText picks the tweet body, preferring `text` over `fullText`.
func xTweetText(src xTweet) string {
	if strings.TrimSpace(src.Text) != "" {
		return src.Text
	}
	return src.FullText
}

// firstNonEmpty returns the first non-blank candidate. Twitter sends the same
// media URL under several spellings depending on the media type and the API
// version, and any of them may be absent.
func firstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if s := strings.TrimSpace(c); s != "" {
			return s
		}
	}
	return ""
}

// parseXTime reads Twitter's legacy createdAt ("Sun Sep 27 13:23:06 +0000
// 2026") and returns the zero time when it does not fit. Kept separate from
// parseActorTime so the ISO layouts and this one cannot silently shadow each
// other; the caller tries ISO first, then this.
func parseXTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(xTimeLayout, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
