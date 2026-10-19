package service

import (
	"fmt"
	"strings"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// commentPermalink builds a comment's own permalink from ids we already store:
// the platform, the post's shortcode (post.external_id) and the comment's
// external id. It is derived, never persisted — comment.external_id already IS
// the id Instagram puts in the /c/<id>/ segment (the actor's latestComments[].id
// is stored verbatim by the ingestor), and post.external_id is the shortcode.
//
// Empty when either id is missing, which is the honest answer: a comment whose
// post shortcode we do not know cannot be addressed, and Enqueue would reject a
// reply built from it. Callers surface that as "no reply offered" rather than
// sending a URL that fails validation.
//
// IG returns the comment id as a bare numeric string, and both /p/ and /reel/
// permalinks address the same comment through /c/ — the post segment in the
// reply URL only has to resolve to the right post, not match how it was scraped.
func commentPermalink(p domain.Platform, postExternalID, commentExternalID string) string {
	code := strings.TrimSpace(postExternalID)
	id := strings.TrimSpace(commentExternalID)
	if code == "" || id == "" {
		return ""
	}
	switch p {
	case domain.PlatformInstagram:
		return fmt.Sprintf("https://www.instagram.com/p/%s/c/%s/", code, id)
	case domain.PlatformThreads:
		// Threads has NO `/post/<post>/c/<comment>/` route — it 404s (verified
		// live against threads.com). A reply there IS a post with its own
		// shortcode, so the addressable permalink for a comment is its own
		// `/post/<comment code>` (which resolves to `/@author/post/<code>`).
		// Replying from it nests the new reply directly under that comment.
		return fmt.Sprintf("https://www.threads.net/post/%s", id)
	}
	return ""
}

// withCommentPermalinks fills Comment.URL for a post's comments. The post code
// is passed in rather than re-read per comment: every comment in the slice
// belongs to the same post, so one lookup covers the batch.
func withCommentPermalinks(p domain.Platform, postExternalID string, comments []domain.Comment) []domain.Comment {
	for i := range comments {
		comments[i].URL = commentPermalink(p, postExternalID, comments[i].ExternalID)
	}
	return comments
}
