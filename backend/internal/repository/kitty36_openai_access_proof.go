package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.OpenAIOAuthAccessProofRepository = (*accountRepository)(nil)

func (r *accountRepository) UpdateOpenAIAccessProofStatusIfUnchanged(ctx context.Context, expected *service.Account, status, message string) (bool, error) {
	if r == nil || r.sql == nil || expected == nil {
		return false, errors.New("account SQL executor or expected state unavailable")
	}
	if scoped := r.accountTransactionScope(ctx); scoped != r {
		applied, err := scoped.UpdateOpenAIAccessProofStatusIfUnchanged(ctx, expected, status, message)
		if applied && err == nil {
			service.AfterAccountCommit(ctx, func(committed context.Context) { r.syncSchedulerAccountSnapshotDetached(committed, expected.ID) })
		}
		return applied, err
	}
	b, err := json.Marshal(normalizeJSONMap(expected.Credentials))
	if err != nil {
		return false, err
	}
	result, err := r.sql.ExecContext(ctx, `WITH updated AS (
		UPDATE accounts AS a SET status=$1,error_message=NULLIF($2,''),updated_at=NOW()
		WHERE a.id=$3 AND a.deleted_at IS NULL AND a.platform='openai' AND a.type='oauth'
		AND a.credentials=$4::jsonb AND a.proxy_id IS NOT DISTINCT FROM $5
		AND a.status=$6 AND COALESCE(a.error_message,'')=$7 AND a.updated_at=$8
		AND a.schedulable=TRUE AND a.parent_account_id IS NULL
		AND NOT EXISTS (SELECT 1 FROM account_proxies p WHERE p.account_id=a.id)
		RETURNING a.id)
		INSERT INTO scheduler_outbox(event_type,account_id,group_id,payload)
		SELECT $9,updated.id,NULL,NULL FROM updated`, status, message, expected.ID, string(b),
		expected.ProxyID, expected.Status, expected.ErrorMessage, expected.UpdatedAt, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, expected.ID)
	return true, nil
}
