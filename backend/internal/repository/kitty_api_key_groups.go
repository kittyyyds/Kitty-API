package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func apiKeyInGroup(id int64) predicate.APIKey {
	return func(s *entsql.Selector) {
		links := entsql.Table("api_key_groups")
		s.Where(entsql.Or(
			entsql.EQ(s.C(apikey.FieldGroupID), id),
			entsql.In(s.C(apikey.FieldID), entsql.Select(links.C("api_key_id")).From(links).Where(entsql.EQ(links.C("group_id"), id))),
		))
	}
}

func isMissingRelationOrColumnErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "no such column") ||
		strings.Contains(msg, "does not exist")
}

func (r *apiKeyRepository) attachGroupIDs(ctx context.Context, keys []service.APIKey) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]any, len(keys))
	placeholders := make([]string, len(keys))
	index := make(map[int64]int, len(keys))
	for i := range keys {
		ids[i], index[keys[i].ID] = keys[i].ID, i
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		keys[i].GroupIDs = nil
		if keys[i].GroupID != nil {
			keys[i].GroupIDs = []int64{*keys[i].GroupID}
		}
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx,
		`SELECT api_key_id, group_id FROM api_key_groups WHERE api_key_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY group_id`, ids...)
	if err != nil {
		if !isMissingRelationOrColumnErr(err) {
			return err
		}
	} else {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var keyID, groupID int64
			if err := rows.Scan(&keyID, &groupID); err != nil {
				return err
			}
			i, ok := index[keyID]
			if ok && (keys[i].GroupID == nil || *keys[i].GroupID != groupID) {
				keys[i].GroupIDs = append(keys[i].GroupIDs, groupID)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}

	// Also hydrate optional fallback_group_id (added in migration 954).
	fbRows, fbErr := client.QueryContext(ctx,
		`SELECT id, fallback_group_id FROM api_keys WHERE id IN (`+strings.Join(placeholders, ",")+`) AND fallback_group_id IS NOT NULL`, ids...)
	if fbErr != nil {
		if !isMissingRelationOrColumnErr(fbErr) {
			return fbErr
		}
		return nil
	}
	defer func() { _ = fbRows.Close() }()
	for fbRows.Next() {
		var keyID int64
		var fbID sql.NullInt64
		if err := fbRows.Scan(&keyID, &fbID); err != nil {
			return err
		}
		if fbID.Valid {
			if i, ok := index[keyID]; ok {
				v := fbID.Int64
				keys[i].FallbackGroupID = &v
			}
		}
	}
	return fbRows.Err()
}

func (r *apiKeyRepository) entityWithGroupIDs(ctx context.Context, entity *dbent.APIKey) (*service.APIKey, error) {
	keys := []service.APIKey{*apiKeyEntityToService(entity)}
	if err := r.attachGroupIDs(ctx, keys); err != nil {
		return nil, err
	}
	return &keys[0], nil
}

func (r *apiKeyRepository) mutateWithGroups(ctx context.Context, key *service.APIKey, mutate func(context.Context) error) error {
	if dbent.TxFromContext(ctx) != nil {
		if err := mutate(ctx); err != nil {
			return err
		}
		if err := r.replaceExtraGroups(ctx, key); err != nil {
			return err
		}
		return r.saveFallbackGroupID(ctx, key)
	}
	tx, err := r.client.Tx(ctx)
	if errors.Is(err, dbent.ErrTxStarted) {
		if err := mutate(ctx); err != nil {
			return err
		}
		if err := r.replaceExtraGroups(ctx, key); err != nil {
			return err
		}
		return r.saveFallbackGroupID(ctx, key)
	}
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := mutate(txCtx); err != nil {
		return err
	}
	if err := r.replaceExtraGroups(txCtx, key); err != nil {
		return err
	}
	if err := r.saveFallbackGroupID(txCtx, key); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *apiKeyRepository) replaceExtraGroups(ctx context.Context, key *service.APIKey) error {
	if key == nil || key.ID <= 0 {
		return nil
	}
	client := clientFromContext(ctx, r.client)
	if _, err := client.ExecContext(ctx, `DELETE FROM api_key_groups WHERE api_key_id = $1`, key.ID); err != nil {
		if isMissingRelationOrColumnErr(err) {
			return nil
		}
		return err
	}
	if key.GroupID == nil {
		return nil
	}
	for _, id := range key.GroupIDs {
		if key.GroupID != nil && *key.GroupID == id {
			continue
		}
		if _, err := client.ExecContext(ctx, `INSERT INTO api_key_groups (api_key_id, group_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, key.ID, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *apiKeyRepository) saveFallbackGroupID(ctx context.Context, key *service.APIKey) error {
	if key == nil || key.ID <= 0 {
		return nil
	}
	client := clientFromContext(ctx, r.client)
	var err error
	if key.FallbackGroupID == nil {
		_, err = client.ExecContext(ctx, `UPDATE api_keys SET fallback_group_id = NULL WHERE id = $1`, key.ID)
	} else {
		_, err = client.ExecContext(ctx, `UPDATE api_keys SET fallback_group_id = $1 WHERE id = $2`, *key.FallbackGroupID, key.ID)
	}
	if isMissingRelationOrColumnErr(err) {
		return nil
	}
	return err
}
