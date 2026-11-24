package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

type SubscriptionRenewalMode string

const (
	SubscriptionRenewalModeRestart SubscriptionRenewalMode = "restart"
	SubscriptionRenewalModeExtend  SubscriptionRenewalMode = "extend"
)

func NormalizeSubscriptionRenewalMode(raw string) (SubscriptionRenewalMode, error) {
	switch SubscriptionRenewalMode(strings.TrimSpace(raw)) {
	case SubscriptionRenewalModeRestart:
		return SubscriptionRenewalModeRestart, nil
	case "", SubscriptionRenewalModeExtend:
		return SubscriptionRenewalModeExtend, nil
	default:
		return "", infraerrors.BadRequest("INVALID_RENEWAL_MODE", "renewal_mode must be 'restart' or 'extend'")
	}
}

func (s *SubscriptionService) assignOrRenewPaidSubscription(
	ctx context.Context,
	input *AssignSubscriptionInput,
	mode SubscriptionRenewalMode,
	deferCacheInvalidation bool,
) (*UserSubscription, bool, error) {
	normalizedMode, err := NormalizeSubscriptionRenewalMode(string(mode))
	if err != nil {
		return nil, false, err
	}
	mode = normalizedMode

	group, err := s.groupRepo.GetByID(ctx, input.GroupID)
	if err != nil {
		return nil, false, fmt.Errorf("group not found: %w", err)
	}
	if !group.IsSubscriptionType() {
		return nil, false, ErrGroupNotSubscriptionType
	}

	existingSub, err := s.userSubRepo.GetByUserIDAndGroupID(ctx, input.UserID, input.GroupID)
	if err != nil {
		existingSub = nil
	}

	validityDays := input.ValidityDays
	if validityDays <= 0 {
		validityDays = 30
	}
	if validityDays > MaxValidityDays {
		validityDays = MaxValidityDays
	}

	if existingSub != nil {
		if mode == SubscriptionRenewalModeRestart {
			err = s.restartExistingSubscriptionTerm(ctx, existingSub.ID, validityDays, input.Notes)
		} else {
			err = s.updateExistingSubscriptionTerm(ctx, existingSub.ID, validityDays, input.Notes, false)
		}
		if err != nil {
			return nil, false, err
		}

		s.maybeInvalidateAssignmentCaches(input.UserID, input.GroupID, deferCacheInvalidation)
		sub, getErr := s.userSubRepo.GetByID(ctx, existingSub.ID)
		return sub, true, getErr
	}

	sub, err := s.createSubscription(ctx, input)
	if err != nil {
		return nil, false, err
	}
	s.maybeInvalidateAssignmentCaches(input.UserID, input.GroupID, deferCacheInvalidation)
	return sub, false, nil
}

func (s *SubscriptionService) restartExistingSubscriptionTerm(ctx context.Context, subscriptionID int64, validityDays int, notes string) error {
	return s.withSubscriptionUpdateTx(ctx, func(txCtx context.Context) error {
		existingSub, err := s.userSubRepo.GetByIDForUpdate(txCtx, subscriptionID)
		if err != nil {
			return fmt.Errorf("lock subscription for renewal restart: %w", err)
		}

		now := time.Now()
		newExpiresAt := now.AddDate(0, 0, validityDays)
		if newExpiresAt.After(MaxExpiresAt) {
			newExpiresAt = MaxExpiresAt
		}

		renewed := *existingSub
		dailyWindowStart := timezone.StartOfDay(now)
		periodicWindowStart := now
		renewed.StartsAt = now
		renewed.ExpiresAt = newExpiresAt
		renewed.Status = SubscriptionStatusActive
		renewed.DailyWindowStart = &dailyWindowStart
		renewed.WeeklyWindowStart = &periodicWindowStart
		renewed.MonthlyWindowStart = &periodicWindowStart
		renewed.DailyUsageUSD = 0
		renewed.WeeklyUsageUSD = 0
		renewed.MonthlyUsageUSD = 0
		if notes != "" {
			if existingSub.Notes == "" {
				renewed.Notes = notes
			} else {
				renewed.Notes = existingSub.Notes + "\n" + notes
			}
		}
		if err := s.userSubRepo.Update(txCtx, &renewed); err != nil {
			return fmt.Errorf("restart subscription renewal: %w", err)
		}
		return nil
	})
}
