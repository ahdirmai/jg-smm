package service

import (
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// The Instagram comment scraper (with includeNestedComments) NESTS replies
// inside their parent — they are not sibling rows. Reading only the top level
// therefore drops every reply, and a post with a busy thread looks exactly like
// a quiet one. This pins the recursion, the parent linkage, and the ordering the
// ingestor depends on.
//
// The fixture mirrors the documented payload shape (commentUrl/id/text/
// ownerUsername/likesCount/repliesCount/replies), captured from a live run.
const igNestedCommentsPayload = `{
  "url": "https://www.instagram.com/p/DdpmBpCEzpX/",
  "shortCode": "DdpmBpCEzpX",
  "ownerUsername": "ahdirmai.id",
  "latestComments": [
    {
      "id": "111", "text": "top level one", "ownerUsername": "alice",
      "likesCount": 3, "repliesCount": 2,
      "replies": [
        {"id": "111-1", "text": "first reply", "ownerUsername": "bob", "likesCount": 0, "repliesCount": 1,
         "replies": [
            {"id": "111-1-1", "text": "nested reply", "ownerUsername": "carol", "likesCount": 1, "repliesCount": 0, "replies": []}
         ]},
        {"id": "111-2", "text": "second reply", "ownerUsername": "dave", "likesCount": 2, "repliesCount": 0, "replies": []}
      ]
    },
    {"id": "222", "text": "top level two", "ownerUsername": "erin", "likesCount": 1, "repliesCount": 0, "replies": []}
  ]
}`

func TestParseIGCommentsWalksNestedReplies(t *testing.T) {
	items, err := parseIGPostItems([]byte(igNestedCommentsPayload), port.KeywordTimeWindow{})
	if err != nil {
		t.Fatalf("parseIGPostItems: %v", err)
	}

	// 1 post + 2 top-level + 3 nested (111-1, 111-1-1, 111-2).
	if len(items) != 6 {
		t.Fatalf("items = %d, want 6 (post + 2 comments + 3 replies)\n%+v", len(items), items)
	}

	byID := map[string]scrapeItem{}
	for _, it := range items {
		if it.CommentOf != "" { // comment/reply rows carry CommentOf
			byID[it.ExternalID] = it
		}
	}

	// Top-level comments must have NO parent.
	for _, id := range []string{"111", "222"} {
		if got := byID[id].ParentExternalID; got != "" {
			t.Errorf("top-level comment %s has parent %q, want empty", id, got)
		}
	}
	// Replies must point at the comment immediately enclosing them.
	wantParent := map[string]string{
		"111-1":   "111",
		"111-2":   "111",
		"111-1-1": "111-1", // a reply to a reply keeps its own parent
	}
	for id, want := range wantParent {
		if got := byID[id].ParentExternalID; got != want {
			t.Errorf("reply %s parent = %q, want %q", id, got, want)
		}
	}

	// ORDER: the ingestor resolves a reply's parent by external id, so every
	// parent must appear BEFORE its child. A reordering here would make the
	// lookup miss and silently store replies as top-level.
	pos := map[string]int{}
	for i, it := range items {
		pos[it.ExternalID] = i
	}
	for child, parent := range wantParent {
		if pos[parent] > pos[child] {
			t.Errorf("parent %s (index %d) must precede child %s (index %d)", parent, pos[parent], child, pos[child])
		}
	}
}

// The public post scraper (what we run today) returns comments with
// `replies: null` and `repliesCount: null`. That must parse as "no replies",
// not as an error and not as a phantom reply — the null is the shape we
// actually receive on the free plan.
func TestParseIGCommentsToleratesNullReplies(t *testing.T) {
	payload := `{
      "shortCode": "DdpmBpCEzpX",
      "latestComments": [
        {"id": "999", "text": "no replies here", "ownerUsername": "someone",
         "likesCount": 0, "repliesCount": null, "replies": null}
      ]
    }`
	items, err := parseIGPostItems([]byte(payload), port.KeywordTimeWindow{})
	if err != nil {
		t.Fatalf("parseIGPostItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (post + 1 comment)", len(items))
	}
	var comment scrapeItem
	for _, it := range items {
		if it.ExternalID == "999" {
			comment = it
		}
	}
	if comment.ParentExternalID != "" {
		t.Errorf("a null-replies comment must be top-level, got parent %q", comment.ParentExternalID)
	}
}

// A reply whose body is blank is dropped like any other blank comment, and its
// children must NOT be orphaned into the result: the walk skips the whole
// subtree, because a nested reply whose parent row was never written has
// nothing to attach to.
func TestParseIGCommentsSkipsBlankParentSubtree(t *testing.T) {
	payload := `{
      "shortCode": "X",
      "latestComments": [
        {"id": "blank", "text": "   ", "ownerUsername": "x", "repliesCount": 1,
         "replies": [{"id": "child", "text": "real text", "ownerUsername": "y", "replies": []}]}
      ]
    }`
	items, err := parseIGPostItems([]byte(payload), port.KeywordTimeWindow{})
	if err != nil {
		t.Fatalf("parseIGPostItems: %v", err)
	}
	for _, it := range items {
		if it.ExternalID == "blank" {
			t.Error("a blank comment must be dropped")
		}
		if it.ExternalID == "child" {
			t.Error("a child of a dropped parent must not be emitted: it would land with a dangling parent ref")
		}
	}
}

// parseIGComments is called from several parse paths (post scraper, keyword
// search). The platform must be carried through so the ingestor keys the row
// on the right platform.
func TestParseIGCommentsCarriesPlatformAndPost(t *testing.T) {
	items, err := parseIGPostItems([]byte(igNestedCommentsPayload), port.KeywordTimeWindow{})
	if err != nil {
		t.Fatalf("parseIGPostItems: %v", err)
	}
	for _, it := range items {
		if it.CommentOf == "" {
			continue
		}
		if it.Platform != string(domain.PlatformInstagram) {
			t.Errorf("%s platform = %q, want instagram", it.ExternalID, it.Platform)
		}
		if it.CommentOf != "DdpmBpCEzpX" {
			t.Errorf("%s commentOf = %q, want the post shortcode", it.ExternalID, it.CommentOf)
		}
	}
}
