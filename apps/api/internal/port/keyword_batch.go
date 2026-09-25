package port

import (
	"context"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// KeywordBatchStore covers the keyword_batch + keyword_batch_post tables.
type KeywordBatchStore interface {
	CreateKeywordBatch(ctx context.Context, b domain.KeywordBatch) (domain.KeywordBatch, error)
	GetKeywordBatch(ctx context.Context, id string) (domain.KeywordBatch, error)
	ListKeywordBatches(ctx context.Context, limit, offset *int) ([]domain.KeywordBatch, error)
	UpdateKeywordBatch(ctx context.Context, b domain.KeywordBatch) (domain.KeywordBatch, error)
	CreateKeywordBatchPost(ctx context.Context, batchID, postID string) error
	ListKeywordBatchPosts(ctx context.Context, batchID string, limit, offset *int) ([]domain.Post, error)
}
