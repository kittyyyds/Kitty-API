package service

import (
	"context"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const ChannelMonitorModeV3 = "v3"

var ErrChannelMonitorInvalidMode = infraerrors.BadRequest("CHANNEL_MONITOR_INVALID_MODE", "channel monitor mode must be v1, v2, or v3")

// V3Active reports whether the V3 component status page is active.
func (r ChannelMonitorRuntime) V2Active() bool {
	return r.Enabled && r.Mode == ChannelMonitorModeV2
}

func (r ChannelMonitorRuntime) V3Active() bool {
	return r.Enabled && r.Mode == ChannelMonitorModeV3
}

func (s *SettingService) SetChannelMonitorMode(ctx context.Context, mode string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case ChannelMonitorModeV1, ChannelMonitorModeV2, ChannelMonitorModeV3:
	default:
		return "", ErrChannelMonitorInvalidMode
	}
	if err := s.settingRepo.Set(ctx, SettingKeyChannelMonitorMode, normalized); err != nil {
		return "", fmt.Errorf("set channel monitor mode: %w", err)
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}
	s.notifyChannelMonitorRuntimeListeners()
	return normalized, nil
}
