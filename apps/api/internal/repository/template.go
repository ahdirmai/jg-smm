package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

// TemplateRepo implements port.TemplateStore over the sqlc query handle. It
// covers the comment pool (P3-02): CRUD plus the dedupe pick that keeps a
// variant from repeating on one target inside 7 days.
type TemplateRepo struct {
	q *sqlcgen.Queries
}

// NewTemplateRepo binds the repo to a sqlc query handle.
func NewTemplateRepo(q *sqlcgen.Queries) *TemplateRepo { return &TemplateRepo{q: q} }

var _ port.TemplateStore = (*TemplateRepo)(nil)

// CreateTemplate inserts one variant. is_active defaults to true: the Go zero
// value (false) would silently override the column DEFAULT and ship a paused
// template, so the create path treats "not set" as active. Validation runs here
// to give the composer a 400 before a DB round trip.
func (r *TemplateRepo) CreateTemplate(ctx context.Context, t domain.CommentTemplate) (domain.CommentTemplate, error) {
	if err := t.Validate(); err != nil {
		return domain.CommentTemplate{}, fmt.Errorf("repository.template.CreateTemplate: %w", err)
	}
	t.IsActive = true
	row, err := r.q.CreateCommentTemplate(ctx, sqlcgen.CreateCommentTemplateParams{
		Platform:    platformEnum(t.Platform),
		Text:        t.Text,
		Vars:        emptyIfNil(t.Vars),
		Weight:      int32(t.Weight),
		BannedWords: emptyIfNil(t.BannedWords),
		IsActive:    t.IsActive,
	})
	if err != nil {
		return domain.CommentTemplate{}, fmt.Errorf("repository.template.CreateTemplate: %w", err)
	}
	return templateDomain(row), nil
}

// GetTemplate returns one variant by id.
func (r *TemplateRepo) GetTemplate(ctx context.Context, id string) (domain.CommentTemplate, error) {
	row, err := r.q.GetCommentTemplateByID(ctx, uuidValue(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.CommentTemplate{}, domain.ErrNotFound
		}
		return domain.CommentTemplate{}, fmt.Errorf("repository.template.GetTemplate: %w", err)
	}
	return templateDomain(row), nil
}

// ListTemplates returns the pool for one platform, newest first. The pick pool
// is always active-only; includeInactive is the dashboard's "show paused"
// toggle so a reviewed variant stays visible without re-entering the pool.
func (r *TemplateRepo) ListTemplates(ctx context.Context, platform domain.Platform, includeInactive bool, limit, offset *int) ([]domain.CommentTemplate, error) {
	l, o := ptrPage(limit, offset)
	// An empty platform is the "all platforms" listing (the dashboard's default
	// view with no platform filter): omit the platform predicate entirely rather
	// than defaulting to one platform and hiding the rest.
	if platform == "" {
		rows, err := r.q.ListAllCommentTemplates(ctx, sqlcgen.ListAllCommentTemplatesParams{
			Column1: includeInactive,
			Limit:   l,
			Offset:  o,
		})
		if err != nil {
			return nil, fmt.Errorf("repository.template.ListTemplates(all): %w", err)
		}
		out := make([]domain.CommentTemplate, 0, len(rows))
		for _, row := range rows {
			out = append(out, templateDomain(row))
		}
		return out, nil
	}
	rows, err := r.q.ListCommentTemplates(ctx, sqlcgen.ListCommentTemplatesParams{
		Platform: platformEnum(platform),
		Column2:  includeInactive,
		Limit:    l,
		Offset:   o,
	})
	if err != nil {
		return nil, fmt.Errorf("repository.template.ListTemplates: %w", err)
	}
	out := make([]domain.CommentTemplate, 0, len(rows))
	for _, row := range rows {
		out = append(out, templateDomain(row))
	}
	return out, nil
}

// UpdateTemplate replaces a variant's mutable fields. Platform is immutable: a
// template is authored against one platform's limits, and retargeting it to
// another platform would silently break tone/length assumptions.
func (r *TemplateRepo) UpdateTemplate(ctx context.Context, t domain.CommentTemplate) (domain.CommentTemplate, error) {
	if err := t.Validate(); err != nil {
		return domain.CommentTemplate{}, fmt.Errorf("repository.template.UpdateTemplate: %w", err)
	}
	row, err := r.q.UpdateCommentTemplate(ctx, sqlcgen.UpdateCommentTemplateParams{
		ID:          uuidValue(t.ID),
		Text:        t.Text,
		Vars:        emptyIfNil(t.Vars),
		Weight:      int32(t.Weight),
		BannedWords: emptyIfNil(t.BannedWords),
		IsActive:    t.IsActive,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.CommentTemplate{}, domain.ErrNotFound
		}
		return domain.CommentTemplate{}, fmt.Errorf("repository.template.UpdateTemplate: %w", err)
	}
	return templateDomain(row), nil
}

// DeleteTemplate removes a variant. ON DELETE SET NULL keeps history readable;
// only the link to the definition is lost.
func (r *TemplateRepo) DeleteTemplate(ctx context.Context, id string) error {
	if err := r.q.DeleteCommentTemplate(ctx, uuidValue(id)); err != nil {
		return fmt.Errorf("repository.template.DeleteTemplate: %w", err)
	}
	return nil
}

// PickForTarget returns the dedupe pool: active templates for the platform that
// have not been used against this target in 7 days, heaviest first. The
// weighted-random step is the service's job — it is testable there and needs
// no SQL seed.
func (r *TemplateRepo) PickForTarget(ctx context.Context, platform domain.Platform, targetID string) ([]domain.CommentTemplate, error) {
	rows, err := r.q.PickForTarget(ctx, sqlcgen.PickForTargetParams{
		Platform: platformEnum(platform),
		TargetID: uuidValue(targetID),
	})
	if err != nil {
		return nil, fmt.Errorf("repository.template.PickForTarget: %w", err)
	}
	out := make([]domain.CommentTemplate, 0, len(rows))
	for _, row := range rows {
		out = append(out, templateDomain(row))
	}
	return out, nil
}

// emptyIfNil swaps a nil slice for an empty one. A nil []string is encoded by
// pgx as SQL NULL, which defeats the column DEFAULT '{}' and trips the
// NOT NULL constraint: the pool treats "no denylist" as an empty denylist.
func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---------------------------------------------------------------------------
// converters
// ---------------------------------------------------------------------------

func templateDomain(row sqlcgen.CommentTemplate) domain.CommentTemplate {
	return domain.CommentTemplate{
		ID:          uuidString(row.ID),
		Platform:    platformDomain(row.Platform),
		Text:        row.Text,
		Vars:        row.Vars,
		Weight:      int(row.Weight),
		BannedWords: row.BannedWords,
		IsActive:    row.IsActive,
		CreatedAt:   tsTimeOrZero(row.CreatedAt),
	}
}
