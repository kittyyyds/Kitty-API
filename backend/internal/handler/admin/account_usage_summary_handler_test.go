//go:build unit

package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const repositoryAccountUsageDrainSQL = "SELECT kitty_drain_account_usage_events($1)"

// Exercise the real route -> summary service -> repository -> JSON boundary.
// Only the database driver is mocked; no backend contract is manually assembled.
func TestAccountUsageSummaryHandlerDatabaseToJSONAndCache(t *testing.T) {
	oldCache := accountTodayStatsBatchCache
	accountTodayStatsBatchCache = newSnapshotCache(time.Minute)
	t.Cleanup(func() { accountTodayStatsBatchCache = oldCache })
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := repository.NewUsageLogRepository(nil, db)
	usage := service.NewAccountUsageService(nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := &AccountHandler{accountUsageService: usage}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/admin/accounts/today-stats/batch", h.GetBatchTodayStats)
	mock.ExpectQuery("(?s)SELECT account_id, COUNT.*FROM usage_logs.*account_id = ANY").
		WillReturnRows(sqlmock.NewRows([]string{"id", "requests", "tokens", "cost", "standard_cost", "user_cost"}).AddRow(42, 2300, 285100000, 102.30, 998, 17.28))
	mock.ExpectQuery("(?s)SELECT account_id, account_cost, user_cost.*kitty_account_lifetime_costs.*account_id = ANY").
		WillReturnRows(sqlmock.NewRows([]string{"id", "cost", "user_cost"}).AddRow(7, 0, 0).AddRow(42, 1023, 172.8))
	mock.ExpectQuery(regexp.QuoteMeta(repositoryAccountUsageDrainSQL)).WithArgs(2048).WillReturnRows(sqlmock.NewRows([]string{"processed"}).AddRow(0))
	mock.ExpectQuery("(?s)WITH pending_accounts AS.*unnest.*kitty_account_lifetime_usage").
		WillReturnRows(sqlmock.NewRows([]string{"id", "requests", "tokens"}).AddRow(42, 23000, 2851000000))
	request := func(body, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/today-stats/batch", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	first := request(`{"account_ids":[42,7,42,-1]}`, "")
	require.Equal(t, http.StatusOK, first.Code)
	var payload struct {
		Data struct {
			Stats map[string]map[string]any `json:"stats"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Stats, 2)
	require.Equal(t, 102.30, payload.Data.Stats["42"]["cost"])
	require.Equal(t, 17.28, payload.Data.Stats["42"]["user_cost"])
	require.Equal(t, 1023.0, payload.Data.Stats["42"]["lifetime_cost"])
	require.Equal(t, 172.8, payload.Data.Stats["42"]["lifetime_user_cost"])
	require.Equal(t, 23000.0, payload.Data.Stats["42"]["lifetime_requests"])
	require.Equal(t, 2851000000.0, payload.Data.Stats["42"]["lifetime_tokens"])
	require.Equal(t, 0.0, payload.Data.Stats["7"]["lifetime_requests"])
	require.Equal(t, 0.0, payload.Data.Stats["7"]["lifetime_tokens"])
	require.Equal(t, 0.0, payload.Data.Stats["7"]["requests"])
	require.Equal(t, 0.0, payload.Data.Stats["7"]["lifetime_cost"])
	require.Equal(t, "miss", first.Header().Get("X-Snapshot-Cache"))
	second := request(`{"account_ids":[7,42]}`, "")
	require.Equal(t, http.StatusOK, second.Code)
	require.JSONEq(t, first.Body.String(), second.Body.String())
	require.Equal(t, "hit", second.Header().Get("X-Snapshot-Cache"))
	cached := request(`{"account_ids":[42,7]}`, first.Header().Get("ETag"))
	require.Equal(t, http.StatusNotModified, cached.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountUsageSummaryHandlerUnavailableSourceRemainsNull(t *testing.T) {
	oldCache := accountTodayStatsBatchCache
	accountTodayStatsBatchCache = newSnapshotCache(time.Minute)
	t.Cleanup(func() { accountTodayStatsBatchCache = oldCache })
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	usage := service.NewAccountUsageService(nil, repository.NewUsageLogRepository(nil, db), nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := &AccountHandler{accountUsageService: usage}
	mock.ExpectQuery("(?s)SELECT account_id, COUNT.*FROM usage_logs").WillReturnError(errors.New("query unavailable"))
	mock.ExpectQuery("(?s)SELECT account_id, account_cost, user_cost.*kitty_account_lifetime_costs").WillReturnRows(sqlmock.NewRows([]string{"id", "cost", "user_cost"}).AddRow(42, 1023, 172.8))
	mock.ExpectQuery(regexp.QuoteMeta(repositoryAccountUsageDrainSQL)).WithArgs(2048).WillReturnRows(sqlmock.NewRows([]string{"processed"}).AddRow(0))
	mock.ExpectQuery("(?s)WITH pending_accounts AS.*kitty_account_lifetime_usage").WillReturnError(errors.New("counter migration not installed"))
	router := gin.New()
	router.POST("/stats", h.GetBatchTodayStats)
	req := httptest.NewRequest(http.MethodPost, "/stats", bytes.NewBufferString(`{"account_ids":[42]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var payload struct {
		Data struct {
			Stats map[string]*service.AccountUsageSummaryStats `json:"stats"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Nil(t, payload.Data.Stats["42"].Requests)
	require.Nil(t, payload.Data.Stats["42"].Cost)
	require.Nil(t, payload.Data.Stats["42"].LifetimeRequests)
	require.Nil(t, payload.Data.Stats["42"].LifetimeTokens)
	require.NotNil(t, payload.Data.Stats["42"].LifetimeCost)
	require.Equal(t, 1023.0, *payload.Data.Stats["42"].LifetimeCost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountUsageSummaryCacheIDsAndDayBoundary(t *testing.T) {
	day := time.Date(2026, 11, 26, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	key := buildAccountUsageSummaryCacheKey([]int64{42, 7, 42, -1}, day)
	require.Equal(t, key, buildAccountUsageSummaryCacheKey([]int64{7, 42}, day))
	require.NotEqual(t, key, buildAccountUsageSummaryCacheKey([]int64{7, 42}, day.AddDate(0, 0, 1)))
	require.NotEqual(t, key, buildAccountUsageSummaryCacheKey([]int64{7, 43}, day))
	require.Equal(t, []int64{42, 7}, func() []int64 { ids := []int64{42, 7}; _ = buildAccountUsageSummaryCacheKey(ids, day); return ids }())
}
