package service

import (
	"context"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
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
	return f.snapshots, nil
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
