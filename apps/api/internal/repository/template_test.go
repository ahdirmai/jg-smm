package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
	"github.com/jackc/pgx/v5/pgxpool"
)

// P3-02 template repository tests. Real Postgres (SMM_TEST_DB=1) because the
// interesting behaviour is the dedupe query — a temporal join a fake cannot
// exercise — plus the schema guards (weight > 0, non-blank text).

func newTemplateTestRepo(t *testing.T) (*TemplateRepo, *ActionRepo) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping db integration test in -short mode")
	}
	if os.Getenv("SMM_TEST_DB") != "1" {
		t.Skip("set SMM_TEST_DB=1 to run repository integration tests")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://smm:smm@localhost:24543/smm?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	q := sqlcgen.New(pool)
	if _, err := pool.Exec(context.Background(), `
		TRUNCATE TABLE
		  action_log, action_job, comment_template,
		  scrape_job, target, comment, post, raw_payload, apify_run,
		  official_account, analytics_snapshot, analytics_mention,
		  analytics_ingest_run, provision_log, worker, account
		CASCADE`); err != nil {
		t.Fatalf("truncate template tables: %v", err)
	}
	return NewTemplateRepo(q), NewActionRepo(q)
}

func TestTemplateCRUD(t *testing.T) {
	tplRepo, _ := newTemplateTestRepo(t)
	ctx := context.Background()

	created, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "loving the {topic} energy!",
		Vars:     []string{"topic"},
		Weight:   3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || !created.IsActive {
		t.Fatalf("new template must get an id and default to active, got %+v", created)
	}
	if created.Weight != 3 {
		t.Fatalf("weight must round-trip, got %d", created.Weight)
	}

	got, err := tplRepo.GetTemplate(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Text != "loving the {topic} energy!" || len(got.Vars) != 1 || got.Vars[0] != "topic" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// Update flips it inactive: it must leave the pool but stay readable.
	updated, err := tplRepo.UpdateTemplate(ctx, domain.CommentTemplate{
		ID:       created.ID,
		Platform: domain.PlatformInstagram,
		Text:     "paused variant {topic}",
		Vars:     []string{"topic"},
		Weight:   1,
		IsActive: false,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.IsActive || updated.Text != "paused variant {topic}" {
		t.Fatalf("update must flip active and rewrite text, got %+v", updated)
	}

	// Active-only listing excludes the paused variant; the includeInactive
	// toggle is the dashboard's "show paused" switch.
	active, err := tplRepo.ListTemplates(ctx, domain.PlatformInstagram, false, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("paused template must not be in the active pool, got %d", len(active))
	}
	both, err := tplRepo.ListTemplates(ctx, domain.PlatformInstagram, true, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(both) != 1 || both[0].IsActive {
		t.Fatalf("includeInactive must surface the paused variant, got %d active=%v", len(both), both[0].IsActive)
	}

	// Another platform's pool is separate: a template is platform-scoped.
	other, err := tplRepo.ListTemplates(ctx, domain.PlatformThreads, true, p3PtrInt(10), p3PtrInt(0))
	if err != nil {
		t.Fatalf("list threads: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("threads pool must be empty, got %d", len(other))
	}

	if err := tplRepo.DeleteTemplate(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := tplRepo.GetTemplate(ctx, created.ID); err != domain.ErrNotFound {
		t.Fatalf("deleted template must be gone, got %v", err)
	}
}

// TestTemplatePickDedupe is the pool guarantee: a template already used against
// a target inside 7 days must not be picked again for that target, while
// staying available for a different target.
func TestTemplatePickDedupe(t *testing.T) {
	tplRepo, actionRepo := newTemplateTestRepo(t)
	ctx := context.Background()
	pool := testPool(t)

	accountID := p2SeedAccount(t, pool, "act-tpl-"+p2Suffix(t))
	targetA := p3SeedTarget(t, pool, "tgt-tpl-a-"+p2Suffix(t))
	targetB := p3SeedTarget(t, pool, "tgt-tpl-b-"+p2Suffix(t))
	workerID := p3SeedWorker(t, pool, "wrk-tpl-"+p2Suffix(t))

	used, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "used variant {topic}",
		Vars:     []string{"topic"},
		Weight:   1,
	})
	if err != nil {
		t.Fatalf("create used: %v", err)
	}
	fresh, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "fresh variant {topic}",
		Vars:     []string{"topic"},
		Weight:   1,
	})
	if err != nil {
		t.Fatalf("create fresh: %v", err)
	}

	// Execute one comment with `used` against target A: a job + an action_log
	// linking the template is what dedupe reads.
	job, err := actionRepo.CreateActionJob(ctx, domain.ActionJob{
		Type:       domain.JobTypeActionComment,
		TargetID:   targetA,
		AccountID:  accountID,
		TemplateID: used.ID,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimed, err := actionRepo.ClaimNextActionJob(ctx, accountID, workerID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := actionRepo.UpsertActionLog(ctx, domain.ActionLog{
		ActionJobID:  claimed.ID,
		Attempt:      claimed.Attempts,
		Status:       domain.AttemptSuccess,
		WorkerID:     workerID,
		TemplateID:   used.ID,
		RenderedText: "used variant launch",
	}); err != nil {
		t.Fatalf("upsert log: %v", err)
	}
	_ = job

	// Pool for target A: `used` is excluded, `fresh` remains.
	pickA, err := tplRepo.PickForTarget(ctx, domain.PlatformInstagram, targetA)
	if err != nil {
		t.Fatalf("pick for A: %v", err)
	}
	if len(pickA) != 1 || pickA[0].ID != fresh.ID {
		t.Fatalf("dedupe must exclude the used template for target A, got %d", len(pickA))
	}

	// Pool for target B is untouched: both are still fresh there.
	pickB, err := tplRepo.PickForTarget(ctx, domain.PlatformInstagram, targetB)
	if err != nil {
		t.Fatalf("pick for B: %v", err)
	}
	if len(pickB) != 2 {
		t.Fatalf("target B must see the full pool, got %d", len(pickB))
	}
}

// TestTemplateSchemaGuards checks the column constraints the composer leans on
// before a DB round trip: weight must be positive and text non-blank.
func TestTemplateSchemaGuards(t *testing.T) {
	tplRepo, _ := newTemplateTestRepo(t)
	ctx := context.Background()

	if _, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "   ",
	}); err == nil {
		t.Fatal("expected error for blank text, got nil")
	}
	// Weight must be positive: an even pool is weight 1, and weight 0 would
	// mean "never pick this", which is what is_active already expresses.
	if _, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "zero weight",
	}); err == nil {
		t.Fatal("expected error for zero weight, got nil")
	}
	// A template with no vars and no placeholders is legal (a fixed comment).
	fixed, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "fixed comment",
		Weight:   1,
	})
	if err != nil {
		t.Fatalf("create fixed: %v", err)
	}
	if !fixed.IsActive {
		t.Fatal("a new template must be active by default")
	}
	// A declared var the text never references is a latent typo and is rejected
	// before a worker can post a raw {placeholder}.
	if _, err := tplRepo.CreateTemplate(ctx, domain.CommentTemplate{
		Platform: domain.PlatformInstagram,
		Text:     "no placeholder here",
		Vars:     []string{"topic"},
		Weight:   1,
	}); err == nil {
		t.Fatal("expected error for declared-but-unused var, got nil")
	}
}
