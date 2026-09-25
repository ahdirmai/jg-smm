package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// fakeScrapeStore is an in-memory port.ScrapeStore for the scheduler/ingestor
// tests. It implements exactly the methods the scheduler calls, plus the list
// helpers; a nil-safe zero value is not needed because every test builds one.
type fakeScrapeStore struct {
	mu         sync.Mutex
	targets    map[string]domain.Target
	posts      map[string]domain.Post
	comments   map[string]domain.Comment
	jobs       []domain.ScrapeJob
	snapshots  []domain.MetricSnapshot
	payloads   map[string][]byte // s3 key -> body
	runPayload map[string][]string
	claimCalls int
	// failSnapshotFor, when set, makes CreateMetricSnapshot fail for this one
	// post id — a failure-injection hook for the aggregator's per-post skip.
	failSnapshotFor string
}

func newFakeScrapeStore() *fakeScrapeStore {
	return &fakeScrapeStore{
		targets:    map[string]domain.Target{},
		posts:      map[string]domain.Post{},
		comments:   map[string]domain.Comment{},
		payloads:   map[string][]byte{},
		runPayload: map[string][]string{},
	}
}

var _ port.ScrapeStore = (*fakeScrapeStore)(nil)

// errInjected is the sentinel the failure-injection hooks return.
var errInjected = errors.New("injected failure")

func (f *fakeScrapeStore) UpsertTarget(ctx context.Context, t domain.Target) (domain.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := string(t.Platform) + "|" + t.ExternalID
	if cur, ok := f.targets[key]; ok {
		cur.URL, cur.Meta = t.URL, t.Meta
		f.targets[key] = cur
		return cur, nil
	}
	t.ID = fakeID("tgt", key)
	f.targets[key] = t
	return t, nil
}
func (f *fakeScrapeStore) GetTarget(ctx context.Context, id string) (domain.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.targets {
		if t.ID == id {
			return t, nil
		}
	}
	return domain.Target{}, domain.ErrNotFound
}
func (f *fakeScrapeStore) GetTargetByExternalID(ctx context.Context, p domain.Platform, ext string) (domain.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.targets[string(p)+"|"+ext]; ok {
		return t, nil
	}
	return domain.Target{}, domain.ErrNotFound
}
func (f *fakeScrapeStore) LinkTargetPost(ctx context.Context, tgt, post string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, t := range f.targets {
		if t.ID == tgt {
			t.PostID = &post
			f.targets[k] = t
		}
	}
	return nil
}
func (f *fakeScrapeStore) LinkTargetComment(ctx context.Context, tgt, cmt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, t := range f.targets {
		if t.ID == tgt {
			t.CommentID = &cmt
			f.targets[k] = t
		}
	}
	return nil
}
func (f *fakeScrapeStore) ListTargets(ctx context.Context, limit, offset *int) ([]domain.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Target, 0, len(f.targets))
	for _, t := range f.targets {
		out = append(out, t)
	}
	return out, nil
}
func (f *fakeScrapeStore) UpsertPost(ctx context.Context, p domain.Post) (domain.Post, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := string(p.Platform) + "|" + p.ExternalID
	if cur, ok := f.posts[key]; ok {
		cur.Text, cur.MediaURLs, cur.Metrics = p.Text, p.MediaURLs, p.Metrics
		f.posts[key] = cur
		return cur, nil
	}
	p.ID = fakeID("post", key)
	f.posts[key] = p
	return p, nil
}
func (f *fakeScrapeStore) GetPost(ctx context.Context, id string) (domain.Post, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.posts {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.Post{}, domain.ErrNotFound
}
func (f *fakeScrapeStore) GetPostByExternalID(ctx context.Context, p domain.Platform, ext string) (domain.Post, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if post, ok := f.posts[string(p)+"|"+ext]; ok {
		return post, nil
	}
	return domain.Post{}, domain.ErrNotFound
}
func (f *fakeScrapeStore) ListPosts(ctx context.Context, limit, offset *int) ([]domain.Post, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Post, 0, len(f.posts))
	for _, p := range f.posts {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeScrapeStore) ListTopPosts(ctx context.Context, p domain.Platform, metric string, limit int) ([]domain.Post, error) {
	return f.ListPosts(ctx, nil, nil)
}
func (f *fakeScrapeStore) ListRecentPosts(ctx context.Context, p domain.Platform, limit int) ([]domain.Post, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Post, 0, len(f.posts))
	for _, post := range f.posts {
		if post.Platform == p {
			out = append(out, post)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ScrapedAt.After(out[j].ScrapedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (f *fakeScrapeStore) UpsertComment(ctx context.Context, c domain.Comment) (domain.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := string(c.Platform) + "|" + c.ExternalID
	if cur, ok := f.comments[key]; ok {
		cur.Text, cur.Metrics, cur.ParentID = c.Text, c.Metrics, c.ParentID
		f.comments[key] = cur
		return cur, nil
	}
	c.ID = fakeID("cmt", key)
	f.comments[key] = c
	return c, nil
}
func (f *fakeScrapeStore) GetComment(ctx context.Context, id string) (domain.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.comments {
		if c.ID == id {
			return c, nil
		}
	}
	return domain.Comment{}, domain.ErrNotFound
}
func (f *fakeScrapeStore) ListCommentsByPost(ctx context.Context, postID string, limit, offset *int) ([]domain.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Comment, 0)
	for _, c := range f.comments {
		if c.PostID == postID {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeScrapeStore) CountCommentsByPost(ctx context.Context, postID string) (int, error) {
	c, _ := f.ListCommentsByPost(context.Background(), postID, nil, nil)
	return len(c), nil
}
func (f *fakeScrapeStore) CreateScrapeJob(ctx context.Context, j domain.ScrapeJob) (domain.ScrapeJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j.Status == "" {
		j.Status = domain.JobStatusPending
	}
	if j.ID == "" {
		j.ID = fakeID("job", string(j.Type)+":"+j.TargetID+":"+j.ScheduledAt.String())
	}
	f.jobs = append(f.jobs, j)
	return j, nil
}
func (f *fakeScrapeStore) GetScrapeJob(ctx context.Context, id string) (domain.ScrapeJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return domain.ScrapeJob{}, domain.ErrNotFound
}
func (f *fakeScrapeStore) ClaimNextScrapeJob(ctx context.Context, accountID string) (domain.ScrapeJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimCalls++
	best := -1
	now := time.Now()
	for i, j := range f.jobs {
		if j.AccountID == nil || *j.AccountID != accountID || j.Status != domain.JobStatusPending {
			continue
		}
		if j.ScheduledAt.After(now) {
			continue
		}
		if best == -1 || j.ScheduledAt.Before(f.jobs[best].ScheduledAt) {
			best = i
		}
	}
	if best == -1 {
		return domain.ScrapeJob{}, domain.ErrNotFound
	}
	f.jobs[best].Status = domain.JobStatusRunning
	f.jobs[best].StartedAt = &now
	f.jobs[best].Attempts++
	return f.jobs[best], nil
}
func (f *fakeScrapeStore) ListScrapeJobs(ctx context.Context, limit, offset *int) ([]domain.ScrapeJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.ScrapeJob, len(f.jobs))
	copy(out, f.jobs)
	return out, nil
}
func (f *fakeScrapeStore) ListScrapeJobsByStatus(ctx context.Context, status domain.JobStatus, limit, offset *int) ([]domain.ScrapeJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.ScrapeJob, 0)
	for _, j := range f.jobs {
		if j.Status == status {
			out = append(out, j)
		}
	}
	return out, nil
}
func (f *fakeScrapeStore) ListPendingScrapeJobsByAccount(ctx context.Context, accountID string, limit int) ([]domain.ScrapeJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.ScrapeJob, 0)
	for _, j := range f.jobs {
		if j.AccountID != nil && *j.AccountID == accountID && j.Status == domain.JobStatusPending {
			out = append(out, j)
		}
	}
	return out, nil
}
func (f *fakeScrapeStore) RescheduleScrapeJob(ctx context.Context, id string, scheduledAt time.Time) (domain.ScrapeJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, j := range f.jobs {
		if j.ID == id {
			f.jobs[i].Status = domain.JobStatusPending
			f.jobs[i].ScheduledAt = scheduledAt
			f.jobs[i].FinishedAt = nil
			return f.jobs[i], nil
		}
	}
	return domain.ScrapeJob{}, domain.ErrNotFound
}
func (f *fakeScrapeStore) CompleteScrapeJob(ctx context.Context, id string, status domain.JobStatus, err *string) (domain.ScrapeJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, j := range f.jobs {
		if j.ID == id {
			f.jobs[i].Status = status
			f.jobs[i].Error = err
			now := time.Now()
			f.jobs[i].FinishedAt = &now
			return f.jobs[i], nil
		}
	}
	return domain.ScrapeJob{}, domain.ErrNotFound
}
func (f *fakeScrapeStore) CreateApifyRun(ctx context.Context, r domain.ApifyRun) (domain.ApifyRun, error) {
	// Postgres generates the id (gen_random_uuid()); the fake mirrors that so
	// the keyword service's ingest keyed off run.ID lands under a real key.
	if r.ID == "" {
		r.ID = fakeID("run", r.ActorID+":"+r.RunID+":"+r.Status)
	}
	return r, nil
}
func (f *fakeScrapeStore) UpdateApifyRun(ctx context.Context, r domain.ApifyRun, finished bool) (domain.ApifyRun, error) {
	return r, nil
}
func (f *fakeScrapeStore) CreateRawPayload(ctx context.Context, p domain.RawPayload) (domain.RawPayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payloads[p.S3Key] = nil
	f.runPayload[p.ApifyRunID] = append(f.runPayload[p.ApifyRunID], p.S3Key)
	return p, nil
}
func (f *fakeScrapeStore) ListRawPayloadsByRunID(ctx context.Context, runID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runPayload[runID], nil
}
func (f *fakeScrapeStore) CreateMetricSnapshot(ctx context.Context, s domain.MetricSnapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSnapshotFor != "" && s.PostID == f.failSnapshotFor {
		return errInjected
	}
	f.snapshots = append(f.snapshots, s)
	return nil
}
func (f *fakeScrapeStore) ListMetricSnapshots(ctx context.Context, postID string, from, to time.Time) ([]domain.MetricSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.MetricSnapshot, 0)
	for _, s := range f.snapshots {
		if s.PostID == postID && !s.TS.Before(from) && s.TS.Before(to) {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *fakeScrapeStore) LatestMetricSnapshots(ctx context.Context) ([]domain.MetricSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// One row per post (the latest), mirroring the real GROUP BY post_id query.
	latest := map[string]domain.MetricSnapshot{}
	for _, s := range f.snapshots {
		cur, ok := latest[s.PostID]
		if !ok || s.TS.After(cur.TS) {
			latest[s.PostID] = s
		}
	}
	out := make([]domain.MetricSnapshot, 0, len(latest))
	for _, s := range latest {
		out = append(out, s)
	}
	return out, nil
}
func (f *fakeScrapeStore) TopPostsByMetric(ctx context.Context, metric string, limit int) ([]domain.MetricSnapshot, error) {
	return f.snapshots, nil
}

// fakeStorage is an in-memory port.RawStorage.
type fakeStorage struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeStorage() *fakeStorage { return &fakeStorage{data: map[string][]byte{}} }

var _ port.RawStorage = (*fakeStorage)(nil)

func (s *fakeStorage) Put(ctx context.Context, key string, p []byte) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	s.data[key] = cp
	return int64(len(p)), nil
}
func (s *fakeStorage) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.data[key]; ok {
		return b, nil
	}
	return nil, domain.ErrNotFound
}

// fakeID builds a stable fake uuid-ish string so tests can look rows up.
func fakeID(prefix, seed string) string {
	return prefix + "-" + stableHex(seed)
}

// stableHex derives 8 hex chars from a seed deterministically.
func stableHex(seed string) string {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(seed); i++ {
		h ^= uint64(seed[i])
		h *= 1099511628211
	}
	const digits = "0123456789abcdef"
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = digits[h%16]
		h /= 16
	}
	return string(out)
}

var _ = sqlcgen.Platform("")

// fakeKeywordBatchStore is in-memory KeywordBatchStore for keyword batch tests.
type fakeKeywordBatchStore struct {
	mu      sync.Mutex
	batches map[string]domain.KeywordBatch
	posts   map[string][]string // batchID -> postIDs
}

func newFakeKeywordBatchStore() *fakeKeywordBatchStore {
	return &fakeKeywordBatchStore{batches: map[string]domain.KeywordBatch{}, posts: map[string][]string{}}
}

var _ port.KeywordBatchStore = (*fakeKeywordBatchStore)(nil)

func (f *fakeKeywordBatchStore) CreateKeywordBatch(_ context.Context, b domain.KeywordBatch) (domain.KeywordBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b.ID == "" {
		b.ID = fakeID("kwb", b.ActorID+":"+string(b.Platform)+":"+b.CreatedAt.String())
	}
	if b.Status == "" {
		b.Status = domain.KeywordBatchPending
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	f.batches[b.ID] = b
	return b, nil
}
func (f *fakeKeywordBatchStore) GetKeywordBatch(_ context.Context, id string) (domain.KeywordBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.batches[id]
	if !ok {
		return domain.KeywordBatch{}, domain.ErrNotFound
	}
	return b, nil
}
func (f *fakeKeywordBatchStore) ListKeywordBatches(_ context.Context, limit, offset *int) ([]domain.KeywordBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.KeywordBatch, 0, len(f.batches))
	for _, b := range f.batches {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	l, o := 100, 0
	if limit != nil {
		l = *limit
	}
	if offset != nil {
		o = *offset
	}
	if o > len(out) {
		return nil, nil
	}
	end := o + l
	if end > len(out) {
		end = len(out)
	}
	return out[o:end], nil
}
func (f *fakeKeywordBatchStore) UpdateKeywordBatch(_ context.Context, b domain.KeywordBatch) (domain.KeywordBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.batches[b.ID]
	if !ok {
		return domain.KeywordBatch{}, domain.ErrNotFound
	}
	if b.Status != "" {
		cur.Status = b.Status
	}
	if b.ApifyRunID != nil {
		cur.ApifyRunID = b.ApifyRunID
	}
	cur.ItemsRead = b.ItemsRead
	cur.PostsCount = b.PostsCount
	cur.CommentsCount = b.CommentsCount
	if b.Error != nil {
		cur.Error = b.Error
	}
	if b.FinishedAt != nil {
		cur.FinishedAt = b.FinishedAt
	} else if b.Status.IsTerminal() && cur.FinishedAt == nil {
		n := time.Now().UTC()
		cur.FinishedAt = &n
	}
	f.batches[b.ID] = cur
	return cur, nil
}
func (f *fakeKeywordBatchStore) CreateKeywordBatchPost(_ context.Context, batchID, postID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts[batchID] = append(f.posts[batchID], postID)
	return nil
}
func (f *fakeKeywordBatchStore) ListKeywordBatchPosts(ctx context.Context, batchID string, limit, offset *int) ([]domain.Post, error) {
	// not used directly — service reads via batches+scrapes; keep stub
	return nil, nil
}
