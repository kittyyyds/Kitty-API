package service

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync"
)

type openAITextQueueSpilloverKey struct{}
type openAIQueueSpilloverPoolKey struct{}

func filterOpenAIQueueSpilloverPool(ctx context.Context, accounts []Account) []Account {
	poolID, scoped := ctx.Value(openAIQueueSpilloverPoolKey{}).(int64)
	if !scoped {
		return accounts
	}
	pool := make([]Account, 0, len(accounts))
	for i := range accounts {
		if openAIStickyAccountMatchesGroup(&accounts[i], &poolID) {
			pool = append(pool, accounts[i])
		}
	}
	return pool
}

// WithOpenAITextQueueSpillover opts a plain HTTP text request in before its
// first upstream attempt. Other callers of the scheduler are unaffected.
func WithOpenAITextQueueSpillover(ctx context.Context, enabled bool) context.Context {
	if !enabled {
		return ctx
	}
	return context.WithValue(ctx, openAITextQueueSpilloverKey{}, true)
}

func (s *OpenAIGatewayService) attachOpenAIQueueAlternative(
	ctx context.Context, groupID *int64, model string, excludedIDs map[int64]struct{}, useUpstreamTokenCost bool,
	selection *AccountSelectionResult, decision OpenAIAccountScheduleDecision,
) {
	_, guardian := openAIGuardianParentAffinityFromContext(ctx)
	if ctx.Value(openAITextQueueSpilloverKey{}) != true || groupID == nil || *groupID <= 0 ||
		selection == nil || selection.Account == nil || selection.Acquired || selection.WaitPlan == nil ||
		selection.UsedGroupFallback || selection.UsedAPIKeyFallback || selection.PreserveStickyBinding ||
		guardian || decision.Layer == openAIAccountScheduleLayerGuardianParent || decision.StickyPreviousHit ||
		StrictFailoverRequestActive(ctx) || strictFailoverStateFrom(ctx) != nil ||
		strings.EqualFold(strings.TrimSpace(model), codexAutoReviewModel) || IsGPTImageGenerationModel(model) {
		return
	}
	poolID := *groupID
	if !openAIStickyAccountMatchesGroup(selection.Account, &poolID) {
		return
	}
	originalExcluded := make(map[int64]struct{}, len(excludedIDs)+1)
	maps.Copy(originalExcluded, excludedIDs)
	originalExcluded[selection.Account.ID] = struct{}{}
	probeExcluded := originalExcluded
	var probeMu sync.Mutex
	selection.TryAcquireQueueAlternative = func(probeCtx context.Context) *AccountSelectionResult {
		if !probeMu.TryLock() {
			return nil
		}
		defer probeMu.Unlock()
		if probeCtx.Err() != nil || openAIProxyStreamQuarantineBypassed(probeCtx) {
			return nil
		}
		// Do not let speculative selection change the forwarding loop's tier state.
		// If the group switched to strict tiers since admission, stop spillover.
		probeCtx = context.WithValue(probeCtx, strictFailoverFailuresKey{}, &strictFailoverFailures{reasons: map[int64]string{}})
		probeCtx = context.WithValue(probeCtx, openAIQueueSpilloverPoolKey{}, poolID)
		rotateBusy := !s.schedulingConfig().LoadBatchEnabled && !s.isOpenAIAdvancedSchedulerEnabled(probeCtx)
		for turn := 0; turn < 2 && probeCtx.Err() == nil; turn++ {
			// One normal same-group selection, with no sticky write, API-key group
			// fallback, or quarantine fail-open. Keep the account/route lease intact.
			other, _, err := s.selectAccountWithSchedulerOnce(probeCtx, &poolID, "", "", model, probeExcluded,
				OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, "", false, PlatformOpenAI, false, useUpstreamTokenCost)
			if err == nil && !StrictFailoverRequestActive(probeCtx) && s.finalizeOpenAIQueueAlternative(probeCtx, &poolID, other) {
				return other
			}
			if other != nil && other.ReleaseFunc != nil {
				other.ReleaseFunc()
			}
			if StrictFailoverRequestActive(probeCtx) {
				return nil
			}
			if errors.Is(err, ErrNoAvailableAccounts) && len(probeExcluded) > len(originalExcluded) {
				probeExcluded = originalExcluded
				continue
			}
			// Non-batch legacy scheduling checks only one candidate. Rotate past
			// busy candidates until a later probe can revisit them.
			if rotateBusy && err == nil && other != nil && other.Account != nil && other.WaitPlan != nil {
				if len(probeExcluded) == len(originalExcluded) {
					probeExcluded = maps.Clone(originalExcluded)
				}
				probeExcluded[other.Account.ID] = struct{}{}
			}
			return nil
		}
		return nil
	}
}

// A rejected alternative must release its own lease, leaving the original
// queue reservation with the caller. Check the hydrated account after the
// profit refresh as well, since membership and proxy configuration can change.
func (s *OpenAIGatewayService) finalizeOpenAIQueueAlternative(ctx context.Context, groupID *int64, selection *AccountSelectionResult) bool {
	if ctx.Err() != nil || selection == nil || !selection.Acquired || selection.ReleaseFunc == nil ||
		!openAIStickyAccountMatchesGroup(selection.Account, groupID) {
		return false
	}
	latest, vetoed, _ := s.ProfitControlVetoLatest(ContextWithSelectionProfitGate(ctx, selection), selection.Account)
	if vetoed || ctx.Err() != nil || !openAIStickyAccountMatchesGroup(latest, groupID) ||
		validateProxyReservation(selection.Account, latest) != nil {
		return false
	}
	selection.Account = latest
	return true
}

// BindStickySessionAfterQueueSpillover can seed missing affinity but never
// moves an existing binding. Use the original selection's CAS observation so
// a newer request completing during the wait also keeps its binding.
func (s *OpenAIGatewayService) BindStickySessionAfterQueueSpillover(ctx context.Context, groupID *int64, sessionHash string, accountID int64) error {
	if sessionHash == "" || accountID <= 0 || preserveOpenAIGuardianParentBinding(ctx, sessionHash) {
		return nil
	}
	if snapshot := openAIStickyBindingSnapshotFromContext(ctx, groupID, sessionHash); snapshot != nil {
		if !snapshot.known || snapshot.accountID > 0 || snapshot.preserveForQualityHold {
			return nil
		}
	} else {
		// A newly generated pool-mode request hash has no selection snapshot.
		ctx = withOpenAIStickyBindingSnapshot(ctx, groupID, sessionHash, 0, true)
	}
	return s.BindStickySession(ctx, groupID, sessionHash, accountID)
}
