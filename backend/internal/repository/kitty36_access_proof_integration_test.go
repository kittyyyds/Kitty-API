//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The integration harness owns a migrated disposable database. Restart and
// concurrency fixtures must not share a single uncommitted transaction.
func newBillingAuditDatabase(t *testing.T) (*sql.DB, *dbent.Client) {
	t.Helper()
	require.NotNil(t, integrationDB)
	return integrationDB, testEntClient(t)
}

func TestKitty36OpenAIAccessProofCAS(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	for _, mode := range []string{"restore", "new_denial", "credentials", "paused", "proxy_pool"} {
		t.Run(mode, func(t *testing.T) {
			r := newAccountRepositoryWithSQL(client, integrationDB, nil)
			a := mustCreateAccount(t, client, &service.Account{
				Name: "kitty-proof-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Status: service.StatusError, ErrorMessage: "synthetic refresh rejection", Schedulable: true,
				Credentials: map[string]any{"access_token": "fixture-access", "chatgpt_account_id": "fixture-workspace"},
			})
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
				_ = client.Account.DeleteOneID(a.ID).Exec(ctx)
			})
			_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET schedulable=TRUE,status='error',error_message='synthetic refresh rejection' WHERE id=$1", a.ID)
			require.NoError(t, err)
			observed, err := r.GetByID(ctx, a.ID)
			require.NoError(t, err)
			_, err = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
			require.NoError(t, err)
			switch mode {
			case "new_denial":
				applied, err := r.UpdateOpenAIAccessProofStatusIfUnchanged(ctx, observed, service.StatusError, "synthetic new denial")
				require.NoError(t, err)
				require.True(t, applied)
			case "credentials":
				_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET credentials=credentials || '{\"access_token\":\"changed-fixture\"}'::jsonb WHERE id=$1", a.ID)
				require.NoError(t, err)
			case "paused":
				_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET schedulable=false WHERE id=$1", a.ID)
				require.NoError(t, err)
			case "proxy_pool":
				proxy, err := client.Proxy.Create().SetName("kitty-proof-fixture").SetProtocol("http").SetHost("127.0.0.1").SetPort(12347).SetStatus(service.StatusActive).Save(ctx)
				require.NoError(t, err)
				t.Cleanup(func() { _ = client.Proxy.DeleteOneID(proxy.ID).Exec(ctx) })
				_, err = integrationDB.ExecContext(ctx, "INSERT INTO account_proxies(account_id,proxy_id) VALUES ($1,$2)", a.ID, proxy.ID)
				require.NoError(t, err)
			}
			applied, err := r.UpdateOpenAIAccessProofStatusIfUnchanged(ctx, observed, service.StatusActive, "")
			require.NoError(t, err)
			require.Equal(t, mode == "restore", applied)
			latest, err := r.GetByID(ctx, a.ID)
			require.NoError(t, err)
			want := service.StatusError
			expectedOutbox := 0
			if applied {
				want = service.StatusActive
				expectedOutbox = 1
			}
			if mode == "new_denial" {
				require.Equal(t, "synthetic new denial", latest.ErrorMessage)
				expectedOutbox = 1
			}
			require.Equal(t, want, latest.Status)
			var outbox int
			require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_outbox WHERE account_id=$1", a.ID).Scan(&outbox))
			require.Equal(t, expectedOutbox, outbox)
		})
	}
}
