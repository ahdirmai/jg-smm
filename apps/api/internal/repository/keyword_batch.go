package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository/sqlcgen"
)

type KeywordBatchRepo struct{ q *sqlcgen.Queries }

func NewKeywordBatchRepo(q *sqlcgen.Queries) *KeywordBatchRepo { return &KeywordBatchRepo{q: q} }

var _ port.KeywordBatchStore = (*KeywordBatchRepo)(nil)

func (r *KeywordBatchRepo) CreateKeywordBatch(ctx context.Context, b domain.KeywordBatch) (domain.KeywordBatch, error) {
	row, err := r.q.CreateKeywordBatch(ctx, sqlcgen.CreateKeywordBatchParams{
		Platform:   platformEnum(b.Platform),
		Keywords:   b.Keywords,
		WindowFrom: tsPtr(b.WindowFrom),
		WindowTo:   tsPtr(b.WindowTo),
		MaxPosts:   int32(b.MaxPosts),
		ActorID:    b.ActorID,
		Status:     string(b.Status),
		CreatedBy:  uuidValuePtr(b.CreatedBy),
	})
	if err != nil {
		return domain.KeywordBatch{}, fmt.Errorf("repository.keyword_batch.Create: %w", err)
	}
	return toKeywordBatch(row), nil
}

func (r *KeywordBatchRepo) GetKeywordBatch(ctx context.Context, id string) (domain.KeywordBatch, error) {
	row, err := r.q.GetKeywordBatchByID(ctx, uuidValue(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.KeywordBatch{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.KeywordBatch{}, fmt.Errorf("repository.keyword_batch.Get: %w", err)
	}
	return toKeywordBatch(row), nil
}

func (r *KeywordBatchRepo) ListKeywordBatches(ctx context.Context, limit, offset *int) ([]domain.KeywordBatch, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListKeywordBatches(ctx, sqlcgen.ListKeywordBatchesParams{Limit: l, Offset: o})
	if err != nil {
		return nil, fmt.Errorf("repository.keyword_batch.List: %w", err)
	}
	out := make([]domain.KeywordBatch, 0, len(rows))
	for _, row := range rows {
		out = append(out, toKeywordBatch(row))
	}
	return out, nil
}

func (r *KeywordBatchRepo) UpdateKeywordBatch(ctx context.Context, b domain.KeywordBatch) (domain.KeywordBatch, error) {
	var fin pgtype.Timestamptz
	if b.FinishedAt != nil {
		fin = tsPtr(b.FinishedAt)
	} else if b.Status.IsTerminal() {
		n := time.Now().UTC()
		fin = pgtype.Timestamptz{Time: n, Valid: true}
	}
	row, err := r.q.UpdateKeywordBatchStatus(ctx, sqlcgen.UpdateKeywordBatchStatusParams{
		ID:            uuidValue(b.ID),
		Status:        string(b.Status),
		ApifyRunID:    uuidValuePtr(b.ApifyRunID),
		ItemsRead:     int32(b.ItemsRead),
		PostsCount:    int32(b.PostsCount),
		CommentsCount: int32(b.CommentsCount),
		Error:         b.Error,
		FinishedAt:    fin,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.KeywordBatch{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.KeywordBatch{}, fmt.Errorf("repository.keyword_batch.Update: %w", err)
	}
	return toKeywordBatch(row), nil
}

func (r *KeywordBatchRepo) CreateKeywordBatchPost(ctx context.Context, batchID, postID string) error {
	if err := r.q.CreateKeywordBatchPost(ctx, sqlcgen.CreateKeywordBatchPostParams{
		KeywordBatchID: uuidValue(batchID),
		PostID:         uuidValue(postID),
	}); err != nil {
		return fmt.Errorf("repository.keyword_batch.CreatePost: %w", err)
	}
	return nil
}

func (r *KeywordBatchRepo) ListKeywordBatchPosts(ctx context.Context, batchID string, limit, offset *int) ([]domain.Post, error) {
	l, o := ptrPage(limit, offset)
	rows, err := r.q.ListKeywordBatchPosts(ctx, sqlcgen.ListKeywordBatchPostsParams{
		KeywordBatchID: uuidValue(batchID),
		Limit:          l,
		Offset:         o,
	})
	if err != nil {
		return nil, fmt.Errorf("repository.keyword_batch.ListPosts: %w", err)
	}
	out := make([]domain.Post, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPost(row))
	}
	return out, nil
}
