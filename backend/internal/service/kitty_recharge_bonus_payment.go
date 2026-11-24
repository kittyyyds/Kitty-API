package service

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *PaymentService) SetRechargeBonusRepo(repo RechargeBonusRepository) {
	if s != nil {
		s.rechargeBonusRepo = repo
	}
}

func (s *PaymentService) ListRechargeBonusRules(ctx context.Context) ([]RechargeBonusRule, error) {
	if s == nil || s.rechargeBonusRepo == nil {
		return []RechargeBonusRule{}, nil
	}
	return s.rechargeBonusRepo.ListActiveRules(ctx, time.Now().UTC())
}

func (s *PaymentService) GetRechargeBonusSummary(ctx context.Context, userID int64) (*RechargeBonusSummary, error) {
	if s == nil || s.rechargeBonusRepo == nil || userID <= 0 {
		return &RechargeBonusSummary{}, nil
	}
	return s.rechargeBonusRepo.GetUserSummary(ctx, userID, time.Now().UTC())
}

func (s *PaymentService) quoteBalanceRecharge(ctx context.Context, cfg *PaymentConfig, amount float64, currency string) (rechargeBonusQuote, *RechargeBonusRule, error) {
	var rule *RechargeBonusRule
	if s.rechargeBonusRepo != nil {
		matched, err := s.rechargeBonusRepo.MatchRule(ctx, amount, time.Now().UTC())
		if err != nil {
			return rechargeBonusQuote{}, nil, fmt.Errorf("match recharge bonus rule: %w", err)
		}
		if matched != nil {
			copyRule := *matched
			rule = &copyRule
		}
	}
	if rule != nil && cfg != nil {
		copyConfig := *cfg
		copyConfig.RechargeBonusTiers = nil
		return quoteRechargeBonus(&copyConfig, amount, currency), rule, nil
	}
	return quoteRechargeBonus(cfg, amount, currency), nil, nil
}

func (s *PaymentService) applyKittyRechargeBonusForOrder(ctx context.Context, o *dbent.PaymentOrder) error {
	if s == nil || s.rechargeBonusRepo == nil || o == nil || o.OrderType != payment.OrderTypeBalance {
		return nil
	}
	_, err := s.rechargeBonusRepo.GrantForOrder(ctx, o.ID, o.UserID, time.Now().UTC())
	return err
}

func (s *PaymentService) revokeKittyRechargeBonusForRefund(ctx context.Context, o *dbent.PaymentOrder, refundAmount float64) (float64, error) {
	if s == nil || s.rechargeBonusRepo == nil || o == nil || o.OrderType != payment.OrderTypeBalance {
		return 0, nil
	}
	return s.rechargeBonusRepo.RevokeForRefund(ctx, o.ID, o.UserID, refundAmount, o.Amount, time.Now().UTC())
}

// KittyRechargeBonusRuleRecord represents a full row in recharge_bonus_rules for admin management.
type KittyRechargeBonusRuleRecord struct {
	ID             int64      `json:"id"`
	RechargeAmount float64    `json:"recharge_amount"`
	BonusAmount    float64    `json:"bonus_amount"`
	ValidityDays   int        `json:"validity_days"`
	Enabled        bool       `json:"enabled"`
	StartsAt       *time.Time `json:"starts_at,omitempty"`
	EndsAt         *time.Time `json:"ends_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type KittyRechargeBonusRuleInput struct {
	RechargeAmount float64    `json:"recharge_amount"`
	BonusAmount    float64    `json:"bonus_amount"`
	ValidityDays   int        `json:"validity_days"`
	Enabled        *bool      `json:"enabled"`
	StartsAt       *time.Time `json:"starts_at"`
	EndsAt         *time.Time `json:"ends_at"`
}

func NormalizeRechargeBonusRuleInput(in KittyRechargeBonusRuleInput) (KittyRechargeBonusRuleInput, error) {
	if math.IsNaN(in.RechargeAmount) || math.IsInf(in.RechargeAmount, 0) || in.RechargeAmount <= 0 {
		return in, infraerrors.BadRequest("INVALID_RECHARGE_AMOUNT", "recharge_amount must be positive")
	}
	if math.IsNaN(in.BonusAmount) || math.IsInf(in.BonusAmount, 0) || in.BonusAmount <= 0 {
		return in, infraerrors.BadRequest("INVALID_BONUS_AMOUNT", "bonus_amount must be positive")
	}
	if in.ValidityDays < 0 || in.ValidityDays > 3650 {
		return in, infraerrors.BadRequest("INVALID_VALIDITY_DAYS", "validity_days must be between 0 and 3650")
	}
	if in.StartsAt != nil && in.EndsAt != nil && !in.EndsAt.After(*in.StartsAt) {
		return in, infraerrors.BadRequest("INVALID_TIME_RANGE", "ends_at must be after starts_at")
	}
	return in, nil
}

type KittyRechargeBonusAdminStore struct {
	db *sql.DB
}

func NewKittyRechargeBonusAdminStore(db *sql.DB) *KittyRechargeBonusAdminStore {
	return &KittyRechargeBonusAdminStore{db: db}
}

func (st *KittyRechargeBonusAdminStore) ListRules(ctx context.Context) ([]KittyRechargeBonusRuleRecord, error) {
	if st == nil || st.db == nil {
		return []KittyRechargeBonusRuleRecord{}, nil
	}
	rows, err := st.db.QueryContext(ctx, `
		SELECT id, recharge_amount, bonus_amount, validity_days, enabled, starts_at, ends_at, created_at, updated_at
		FROM recharge_bonus_rules
		ORDER BY recharge_amount ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []KittyRechargeBonusRuleRecord{}
	for rows.Next() {
		var r KittyRechargeBonusRuleRecord
		var startsAt, endsAt sql.NullTime
		if err := rows.Scan(&r.ID, &r.RechargeAmount, &r.BonusAmount, &r.ValidityDays, &r.Enabled, &startsAt, &endsAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if startsAt.Valid {
			t := startsAt.Time
			r.StartsAt = &t
		}
		if endsAt.Valid {
			t := endsAt.Time
			r.EndsAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (st *KittyRechargeBonusAdminStore) CreateRule(ctx context.Context, in KittyRechargeBonusRuleInput) (*KittyRechargeBonusRuleRecord, error) {
	norm, err := NormalizeRechargeBonusRuleInput(in)
	if err != nil {
		return nil, err
	}
	enabled := true
	if norm.Enabled != nil {
		enabled = *norm.Enabled
	}
	var r KittyRechargeBonusRuleRecord
	var startsAt, endsAt sql.NullTime
	err = st.db.QueryRowContext(ctx, `
		INSERT INTO recharge_bonus_rules (recharge_amount, bonus_amount, validity_days, enabled, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, recharge_amount, bonus_amount, validity_days, enabled, starts_at, ends_at, created_at, updated_at`,
		norm.RechargeAmount, norm.BonusAmount, norm.ValidityDays, enabled, norm.StartsAt, norm.EndsAt,
	).Scan(&r.ID, &r.RechargeAmount, &r.BonusAmount, &r.ValidityDays, &r.Enabled, &startsAt, &endsAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create recharge bonus rule: %w", err)
	}
	if startsAt.Valid {
		t := startsAt.Time
		r.StartsAt = &t
	}
	if endsAt.Valid {
		t := endsAt.Time
		r.EndsAt = &t
	}
	return &r, nil
}

func (st *KittyRechargeBonusAdminStore) UpdateRule(ctx context.Context, id int64, in KittyRechargeBonusRuleInput) (*KittyRechargeBonusRuleRecord, error) {
	norm, err := NormalizeRechargeBonusRuleInput(in)
	if err != nil {
		return nil, err
	}
	enabled := true
	if norm.Enabled != nil {
		enabled = *norm.Enabled
	}
	var r KittyRechargeBonusRuleRecord
	var startsAt, endsAt sql.NullTime
	err = st.db.QueryRowContext(ctx, `
		UPDATE recharge_bonus_rules
		SET recharge_amount=$2, bonus_amount=$3, validity_days=$4, enabled=$5, starts_at=$6, ends_at=$7, updated_at=NOW()
		WHERE id=$1
		RETURNING id, recharge_amount, bonus_amount, validity_days, enabled, starts_at, ends_at, created_at, updated_at`,
		id, norm.RechargeAmount, norm.BonusAmount, norm.ValidityDays, enabled, norm.StartsAt, norm.EndsAt,
	).Scan(&r.ID, &r.RechargeAmount, &r.BonusAmount, &r.ValidityDays, &r.Enabled, &startsAt, &endsAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update recharge bonus rule: %w", err)
	}
	if startsAt.Valid {
		t := startsAt.Time
		r.StartsAt = &t
	}
	if endsAt.Valid {
		t := endsAt.Time
		r.EndsAt = &t
	}
	return &r, nil
}

func (st *KittyRechargeBonusAdminStore) DeleteRule(ctx context.Context, id int64) error {
	_, err := st.db.ExecContext(ctx, `DELETE FROM recharge_bonus_rules WHERE id = $1`, id)
	return err
}

func (st *KittyRechargeBonusAdminStore) Overview(ctx context.Context) (map[string]any, error) {
	var activeGrants int64
	var availableAmount, totalGranted, totalConsumed, totalExpired float64
	err := st.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'active' AND remaining_amount > 0),
			COALESCE(SUM(remaining_amount) FILTER (WHERE status = 'active'), 0),
			COALESCE(SUM(bonus_amount), 0),
			COALESCE(SUM(consumed_amount), 0),
			COALESCE(SUM(expired_amount), 0)
		FROM recharge_bonus_grants`).Scan(&activeGrants, &availableAmount, &totalGranted, &totalConsumed, &totalExpired)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"active_grants":    activeGrants,
		"available_amount": availableAmount,
		"total_granted":    totalGranted,
		"total_consumed":   totalConsumed,
		"total_expired":    totalExpired,
	}, nil
}
