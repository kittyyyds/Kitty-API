package service

import (
	"context"
	"fmt"
)

// APIKeyQueuePolicy describes per-key concurrency queueing limits on this server.
type APIKeyQueuePolicy struct {
	MaxWaiting     int
	TimeoutSeconds int
}

// APIKeyQueueCounts holds real-time active and waiting request counts for one API key.
type APIKeyQueueCounts struct {
	Active  int
	Waiting int
}

// APIKeyQueuePolicy returns this server's effective API key queue policy.
func (s *APIKeyService) APIKeyQueuePolicy() APIKeyQueuePolicy {
	return APIKeyQueuePolicy{
		MaxWaiting:     0,
		TimeoutSeconds: 0,
	}
}

// GetAPIKeyQueueStatsBatch returns active/waiting counts for the given API keys.
func (s *APIKeyService) GetAPIKeyQueueStatsBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]APIKeyQueueCounts, error) {
	if s == nil || s.concurrencyService == nil {
		return nil, fmt.Errorf("api key queue statistics unavailable")
	}
	active, err := s.concurrencyService.GetAPIKeyConcurrencyBatch(ctx, apiKeyIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]APIKeyQueueCounts, len(apiKeyIDs))
	for _, id := range apiKeyIDs {
		out[id] = APIKeyQueueCounts{
			Active:  active[id],
			Waiting: 0,
		}
	}
	return out, nil
}
