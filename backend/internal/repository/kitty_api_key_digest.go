package repository

import (
	"context"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/user"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func kittyAPIKeyDigestMatch(raw string) predicate.APIKey {
	digest := service.KittyAPIKeyDigest(raw)
	return func(s *entsql.Selector) {
		s.Where(entsql.P(func(b *entsql.Builder) {
			b.WriteString("substr(").Ident(s.C(apikey.FieldKey)).WriteString(", 9, 64) = ").Arg(digest)
		}))
	}
}

func kittyAPIKeyCredentialPredicate(raw string) predicate.APIKey {
	return apikey.Or(
		apikey.KeyEQ(raw),
		kittyAPIKeyDigestMatch(raw),
	)
}

func kittyAPIKeyExistsPredicate(raw string) predicate.APIKey {
	return apikey.Or(
		apikey.KeyEQ(raw),
		apikey.KeyEQ(service.KittyAPIKeyStoredValue(raw)),
		kittyAPIKeyDigestMatch(raw),
	)
}

func isRejectedRawAPIKeyCredential(raw string) bool {
	return raw == "" || strings.HasPrefix(raw, "$sha256$")
}

func (r *apiKeyRepository) KittyRotateCredential(ctx context.Context, id, userID int64, previous, raw string) error {
	n, err := r.client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.UserIDEQ(userID), apikey.KeyEQ(previous), apikey.DeletedAtIsNil()).
		SetKey(raw).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrAPIKeyExists)
	}
	if n != 1 {
		return infraerrors.Conflict("API_KEY_CHANGED", "API key changed; refresh before regenerating")
	}
	return nil
}

func (r *apiKeyRepository) KittyGetByIDForAuth(ctx context.Context, id int64) (*service.APIKey, error) {
	m, err := r.activeQuery().
		Where(apikey.IDEQ(id)).
		WithUser(func(q *dbent.UserQuery) {
			q.Select(
				user.FieldID,
				user.FieldEmail,
				user.FieldUsername,
				user.FieldStatus,
				user.FieldRole,
				user.FieldBalance,
				user.FieldConcurrency,
				user.FieldBalanceNotifyEnabled,
				user.FieldRestrictPublicGroups,
				user.FieldBalanceNotifyThresholdType,
				user.FieldBalanceNotifyThreshold,
				user.FieldBalanceNotifyExtraEmails,
				user.FieldTotalRecharged,
				user.FieldSignupSource,
				user.FieldLastLoginAt,
				user.FieldLastActiveAt,
				user.FieldRpmLimit,
			)
			q.WithAllowedGroups(func(gq *dbent.GroupQuery) {
				gq.Select(group.FieldID)
			})
		}).
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return r.entityWithGroupIDs(ctx, m)
}
