package port

import (
	"context"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

// TemplateStore (P3-02) covers the comment pool: CRUD over the templates, plus
// the dedupe pick that decides which variant a comment against one target may
// use. The pick's weighted-random step lives in the service, not here: the
// store only returns the eligible candidate set, keeping the query honest and
// the weighting testable.
type TemplateStore interface {
	CreateTemplate(ctx context.Context, t domain.CommentTemplate) (domain.CommentTemplate, error)
	GetTemplate(ctx context.Context, id string) (domain.CommentTemplate, error)
	ListTemplates(ctx context.Context, platform domain.Platform, includeInactive bool, limit, offset *int) ([]domain.CommentTemplate, error)
	UpdateTemplate(ctx context.Context, t domain.CommentTemplate) (domain.CommentTemplate, error)
	DeleteTemplate(ctx context.Context, id string) error
	// PickForTarget returns the active templates for the platform that have NOT
	// been used against this target in the last 7 days, heaviest first.
	PickForTarget(ctx context.Context, platform domain.Platform, targetID string) ([]domain.CommentTemplate, error)
}
