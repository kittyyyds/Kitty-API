package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIQueueSpilloverSchedulerModes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		advanced bool
		batch    bool
	}{
		{"advanced", true, true}, {"legacy batch", false, true}, {"legacy single", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
			const session = "queue-spillover-session"
			groupID := int64(9230)
			owner := capacitySchedulerAccount(1, groupID, 1, 0)
			busy := capacitySchedulerAccount(2, groupID, 1, 1)
			spare := capacitySchedulerAccount(3, groupID, 1, 2)
			foreign := capacitySchedulerAccount(4, groupID+1, 1, 0)
			excluded := map[int64]struct{}{99: {}}
			availability := map[int64]bool{owner.ID: false, busy.ID: false, spare.ID: false, foreign.ID: true}
			load := map[int64]*AccountLoadInfo{
				owner.ID: {AccountID: owner.ID, CurrentConcurrency: 1, LoadRate: 100},
				busy.ID:  {AccountID: busy.ID, CurrentConcurrency: 1, LoadRate: 100},
				spare.ID: {AccountID: spare.ID, CurrentConcurrency: 1, LoadRate: 100},
			}
			sessions := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:" + session: owner.ID}}
			cfg := capacitySchedulerConfig(1)
			cfg.Gateway.Scheduling.LoadBatchEnabled = tc.batch
			setting := "false"
			if tc.advanced {
				setting = "true"
			}
			var releases []int64
			svc := &OpenAIGatewayService{
				accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{owner, busy, spare, foreign}}},
				cache:       sessions, cfg: cfg, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(setting, "false"),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{
					loadMap: load, acquireResults: availability, releasedIDs: &releases,
				}),
			}
			// The fixture runs two polls back-to-back; production spaces them out.
			svc.concurrencyService.SetAccountLoadBatchCacheTTL(0)
			ctx := WithOpenAITextQueueSpillover(context.Background(), true)
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", session, "gpt-5.1", excluded,
				OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true, PlatformOpenAI)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.WaitPlan)
			require.Equal(t, owner.ID, selection.Account.ID)
			require.NotNil(t, selection.TryAcquireQueueAlternative)
			require.Nil(t, selection.TryAcquireQueueAlternative(ctx), "neither a busy nor an out-of-group account can be admitted")
			availability[spare.ID] = true
			load[spare.ID] = &AccountLoadInfo{AccountID: spare.ID}
			other := selection.TryAcquireQueueAlternative(ctx)
			require.NotNil(t, other)
			require.True(t, other.Acquired)
			require.Equal(t, spare.ID, other.Account.ID)
			require.NotNil(t, other.ReleaseFunc)
			other.ReleaseFunc()
			require.Equal(t, []int64{spare.ID}, releases)
			require.Equal(t, map[int64]struct{}{99: {}}, excluded)
			require.Equal(t, owner.ID, sessions.sessionBindings["openai:"+session])
		})
	}
}

type queueSpilloverProxyLease struct {
	accountID, proxyID int64
	requestID          string
}

type queueSpilloverProxyCache struct {
	schedulerTestConcurrencyCache
	selected           int64
	acquired, released []queueSpilloverProxyLease
}

func (c *queueSpilloverProxyCache) AcquireAccountProxyPoolSlot(_ context.Context, accountID int64, _ []int64, _ int, requestID string, _ ...int64) (int64, error) {
	if c.selected > 0 {
		c.acquired = append(c.acquired, queueSpilloverProxyLease{accountID, c.selected, requestID})
	}
	return c.selected, nil
}

func (c *queueSpilloverProxyCache) ReleaseAccountProxyPoolSlot(_ context.Context, accountID, proxyID int64, requestID string) error {
	c.released = append(c.released, queueSpilloverProxyLease{accountID, proxyID, requestID})
	return nil
}

func TestOpenAIQueueSpilloverProxyAndQualityAdmission(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	groupID := int64(9233)
	owner := capacitySchedulerAccount(1, groupID, 1, 0)
	spare := capacitySchedulerAccount(2, groupID, 1, 1)
	spare.ProxyIDs = []int64{11, 22}
	spare.ProxyID = &spare.ProxyIDs[0]
	spare.Proxies = []*Proxy{{ID: 11, Status: StatusActive}, {ID: 22, Status: StatusActive}}
	var legacyAttempts []int64
	slots := &queueSpilloverProxyCache{schedulerTestConcurrencyCache: schedulerTestConcurrencyCache{
		acquiredIDs:    &legacyAttempts,
		acquireResults: map[int64]bool{owner.ID: false, spare.ID: false},
		loadMap: map[int64]*AccountLoadInfo{
			owner.ID: {AccountID: owner.ID, CurrentConcurrency: 1, LoadRate: 100},
			spare.ID: {AccountID: spare.ID, CurrentConcurrency: 2, LoadRate: 100},
		},
	}}
	cfg := capacitySchedulerConfig(1)
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &OpenAIGatewayService{
		accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{owner, spare}}},
		cfg:         cfg, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("false", "false"),
		concurrencyService: NewConcurrencyService(slots),
	}
	svc.concurrencyService.SetAccountLoadBatchCacheTTL(0)
	qualityHeld := true
	svc.concurrencyService.SetAccountAdmissionGate(accountAdmissionGateFunc(func(_ context.Context, id int64) (bool, error) {
		return !qualityHeld || id != spare.ID, nil
	}))
	ctx := WithOpenAITextQueueSpillover(context.Background(), true)
	selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", "gpt-5.1", nil,
		OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true, PlatformOpenAI)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.TryAcquireQueueAlternative)
	require.Equal(t, owner.ID, selection.Account.ID)
	slots.selected = 22
	slots.loadMap[spare.ID] = &AccountLoadInfo{AccountID: spare.ID}
	require.Nil(t, selection.TryAcquireQueueAlternative(ctx))
	rejectedLeases := len(slots.acquired)
	require.Positive(t, rejectedLeases)
	require.Equal(t, slots.acquired, slots.released, "each quality veto releases its exact proxy reservation")

	qualityHeld = false
	other := selection.TryAcquireQueueAlternative(ctx)
	require.NotNil(t, other)
	require.Equal(t, spare.ID, other.Account.ID)
	require.Equal(t, int64(22), other.Account.SelectedProxyID)
	require.Equal(t, int64(22), other.Account.Proxy.ID)
	require.NotContains(t, legacyAttempts, spare.ID)
	require.Zero(t, spare.SelectedProxyID)
	other.ReleaseFunc()
	other.ReleaseFunc()
	require.Len(t, slots.acquired, rejectedLeases+1)
	require.Equal(t, slots.acquired, slots.released)
}

type queueSpilloverSnapshotCache struct {
	SchedulerCache
	account *Account
}

func (c queueSpilloverSnapshotCache) GetAccount(context.Context, int64) (*Account, error) {
	return c.account, nil
}

func TestOpenAIQueueSpilloverChecksLatestAdmission(t *testing.T) {
	groupID := int64(9234)
	for _, tc := range []struct {
		name       string
		latestRate float64
		groupID    int64
		accepted   bool
	}{
		{"rate still eligible", 0.3, groupID, true},
		{"rate rose after reserving", 0.8, groupID, false},
		{"account left the group", 0.3, groupID + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := capacitySchedulerAccount(2, groupID, 1, 1)
			profitControlTestAccountWithRate(&original, 0.3)
			latest := capacitySchedulerAccount(2, tc.groupID, 1, 1)
			profitControlTestAccountWithRate(&latest, tc.latestRate)
			latest.UpdatedAt = time.Now()
			svc := &OpenAIGatewayService{schedulerSnapshot: &SchedulerSnapshotService{
				cache: queueSpilloverSnapshotCache{account: &latest},
			}}
			ctx := profitControlTestCtx(profitControlTestGroup(groupID, 0.5, 0))
			ctx, _ = svc.WithOpenAIRequestPricingContext(ctx, &groupID)
			selection := &AccountSelectionResult{Account: &original, Acquired: true, ReleaseFunc: func() {}}
			require.Equal(t, tc.accepted, svc.finalizeOpenAIQueueAlternative(ctx, &groupID, selection))
			if tc.accepted {
				require.Equal(t, latest.UpdatedAt, selection.Account.UpdatedAt)
			} else {
				require.Same(t, &original, selection.Account)
			}
		})
	}
}

func TestOpenAIQueueSpilloverStickyAdmissionPreservesOwner(t *testing.T) {
	for _, tc := range []struct {
		name          string
		originalOwner int64
		currentOwner  int64
		wantOwner     int64
		known         bool
	}{
		{"new binding", 0, 0, 2, true},
		{"original binding", 1, 1, 1, true},
		{"newer binding during wait", 0, 3, 3, true},
		{"failed binding read", 0, 3, 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(9231)
			const session = "sticky-while-queued"
			cacheKey := "openai:" + session
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{}}
			if tc.currentOwner > 0 {
				cache.sessionBindings[cacheKey] = tc.currentOwner
			}
			svc := &OpenAIGatewayService{cache: cache}
			ctx := withOpenAIStickyBindingSnapshot(context.Background(), &groupID, session, tc.originalOwner, tc.known)
			require.NoError(t, svc.BindStickySessionAfterQueueSpillover(ctx, &groupID, session, 2))
			require.Equal(t, tc.wantOwner, cache.sessionBindings[cacheKey])
		})
	}
}

func TestOpenAIQueueSpilloverGuards(t *testing.T) {
	groupID := int64(9232)
	account := capacitySchedulerAccount(1, groupID, 1, 0)
	plain := AccountSelectionResult{Account: &account, WaitPlan: &AccountWaitPlan{Timeout: time.Second}}
	ctx := WithOpenAITextQueueSpillover(context.Background(), true)
	strictCtx := context.WithValue(ctx, strictFailoverFailuresKey{}, &strictFailoverFailures{active: true})
	for _, tc := range []struct {
		name, model string
		ctx         context.Context
		decision    OpenAIAccountScheduleDecision
		mutate      func(*AccountSelectionResult)
	}{
		{name: "not opted in", ctx: context.Background()},
		{name: "strict tiers", ctx: strictCtx},
		{name: "image", model: "gpt-image-2"},
		{name: "review", model: codexAutoReviewModel},
		{name: "previous response", decision: OpenAIAccountScheduleDecision{StickyPreviousHit: true}},
		{name: "parent affinity", decision: OpenAIAccountScheduleDecision{Layer: openAIAccountScheduleLayerGuardianParent}},
		{name: "group fallback", mutate: func(r *AccountSelectionResult) { r.UsedGroupFallback = true }},
		{name: "key fallback", mutate: func(r *AccountSelectionResult) { r.UsedAPIKeyFallback = true }},
		{name: "explicit binding preservation", mutate: func(r *AccountSelectionResult) { r.PreserveStickyBinding = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			localCtx := tc.ctx
			if localCtx == nil {
				localCtx = ctx
			}
			selection := plain
			if tc.mutate != nil {
				tc.mutate(&selection)
			}
			(&OpenAIGatewayService{}).attachOpenAIQueueAlternative(localCtx, &groupID, tc.model, nil, true, &selection, tc.decision)
			require.Nil(t, selection.TryAcquireQueueAlternative)
		})
	}
}
