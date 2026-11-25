//go:build unit

package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type geminiAllowlistAccountRepoStub struct {
	gatewayModelsAccountRepoStub
}

func (s *geminiAllowlistAccountRepoStub) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]service.Account, error) {
	allowed := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		allowed[platform] = struct{}{}
	}
	accounts := s.byGroup[groupID]
	filtered := make([]service.Account, 0, len(accounts))
	for _, account := range accounts {
		if _, ok := allowed[account.Platform]; ok {
			filtered = append(filtered, account)
		}
	}
	return filtered, nil
}
