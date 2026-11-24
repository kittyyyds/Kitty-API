package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Kitty ships the imported ranxi audit stack with every switch off. These tests pin
// the defaults that differ from ranxi so an upstream sync cannot silently turn
// them back on.

func TestKittyAuditDefaultsAreAllOffWhenNothingIsSaved(t *testing.T) {
	svc := NewContentModerationService(&contentModerationTestSettingRepo{values: map[string]string{}}, nil, nil, nil, nil, nil, nil, nil)

	view, err := svc.GetConfig(context.Background())
	require.NoError(t, err)
	require.False(t, view.Enabled, "content audit must start disabled")
	require.False(t, view.AutoBanEnabled, "automatic account bans must be opt-in")
	require.True(t, view.CyberPolicyExcludeFromBanCount, "cyber_policy refusals must not count toward a ban")
	require.False(t, view.AllGroups, "the audit scope must not be preset")
	require.Empty(t, view.GroupIDs)
	require.False(t, view.PreHashCheckEnabled)
	require.Empty(t, view.BlockedKeywords, "no keyword list is preset")
}

func TestKittyAuditCannotBeEnabledWithoutChoosingAScope(t *testing.T) {
	svc := NewContentModerationService(&contentModerationTestSettingRepo{values: map[string]string{}}, nil, nil, nil, nil, nil, nil, nil)

	enabled := true
	_, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{Enabled: &enabled})
	require.Error(t, err)
	require.Contains(t, err.Error(), "INVALID_CONTENT_MODERATION_SCOPE")

	allGroups := true
	view, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{Enabled: &enabled, AllGroups: &allGroups})
	require.NoError(t, err)
	require.True(t, view.Enabled)
	require.True(t, view.AllGroups)
}

func TestKittyAuditSavedChoicesOverrideTheBaseline(t *testing.T) {
	repo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyContentModerationConfig: `{"auto_ban_enabled":true,"cyber_policy_exclude_from_ban_count":false,"all_groups":true}`,
	}}
	svc := NewContentModerationService(repo, nil, nil, nil, nil, nil, nil, nil)

	view, err := svc.GetConfig(context.Background())
	require.NoError(t, err)
	require.True(t, view.AutoBanEnabled)
	require.False(t, view.CyberPolicyExcludeFromBanCount)
	require.True(t, view.AllGroups)
}

func TestKittySecurityPenaltyDefaultsToObserveOnlyAndDisabled(t *testing.T) {
	cfg := DefaultSecurityPenaltyConfig()
	require.False(t, cfg.Enabled)
	require.True(t, cfg.ObserveOnly)
	require.False(t, cfg.Applies(1), "a disabled penalty configuration applies to nobody")
}
