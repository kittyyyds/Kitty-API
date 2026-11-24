package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.SchedulerConfiguredPoolRepository = (*accountRepository)(nil)

func (r *accountRepository) ListSchedulerPoolCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]service.Account, error) {
	return r.ListModelAvailabilityCandidates(ctx, groupID, platforms, includeGrouped)
}
