package service

import (
	"context"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// listAllWorkers reads the complete fleet within the repository's 500-row page
// limit. A partial inventory must never reach provisioning or orphan cleanup.
func listAllWorkers(ctx context.Context, store port.WorkerStore) ([]domain.Worker, error) {
	const pageSize = 500
	var workers []domain.Worker
	for offset := 0; ; offset += pageSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := store.List(ctx, port.WorkerFilter{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		workers = append(workers, page...)
		if len(page) < pageSize {
			return workers, nil
		}
	}
}
