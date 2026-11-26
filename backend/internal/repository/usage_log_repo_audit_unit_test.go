//go:build unit

package repository

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func usageAuditFixture() *service.UsageAudit {
	zero, input := 0, 123
	return &service.UsageAudit{
		Version: service.UsageAuditVersion, Status: service.UsageAuditStatusUnverifiable,
		Confidence: service.UsageAuditConfidenceLow, EvidenceSource: service.UsageAuditEvidenceNormalized,
		Protocol: "gemini_generate_content", Reasons: []string{"upstream_usage_missing", "tool_call_without_usage"},
		Upstream:         service.UsageAuditTokenSnapshot{InputTokens: &input, CacheReadTokens: &zero},
		GeminiUsage:      &service.GeminiUsageEvidence{Frames: 2, ToolCallEvents: 1, CacheReadTokens: &zero},
		UpstreamRawUsage: json.RawMessage(`{"promptTokenCount":123,"cachedContentTokenCount":0}`),
	}
}

func usageAuditLogFixture(audit *service.UsageAudit) *service.UsageLog {
	requestID, sessionID, cost := "upstream-request", "client-session", 2.25
	return &service.UsageLog{
		UserID: 1, APIKeyID: 2, AccountID: 3, Model: "gemini-test", InputTokens: 123,
		OutputTokens: 7, TotalCost: 1, ActualCost: 1, UsageAudit: audit,
		AccountStatsCost: &cost, UpstreamRequestID: &requestID, SessionID: &sessionID,
		NativeCompactionV2: true, CacheCreation30mTokens: 4, CacheCreation12hTokens: 5,
		CreatedAt: time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC),
	}
}

func usageAuditColumnIndex(t *testing.T) int {
	t.Helper()
	cols := strings.Split(usageLogSelectColumns, ", ")
	for i, col := range cols {
		if col == "usage_audit" {
			require.Equal(t, "account_stats_cost", cols[i-1])
			require.Equal(t, "upstream_request_id", cols[i+1])
			require.Equal(t, "jsonb", usageLogInsertArgTypes[i-1])
			return i - 1 // SELECT prepends id to the INSERT columns.
		}
	}
	t.Fatal("usage_audit missing from SELECT columns")
	return -1
}

func usageAuditSQLRow(t *testing.T, id int64, log *service.UsageLog, audit any) []driver.Value {
	t.Helper()
	prepared := prepareUsageLogInsert(log)
	values := []driver.Value{id}
	for _, arg := range prepared.args {
		value, err := driver.DefaultParameterConverter.ConvertValue(arg)
		require.NoError(t, err)
		values = append(values, value)
	}
	values[usageAuditColumnIndex(t)+1] = audit
	return values
}

func TestUsageAuditSingleInsertReadRoundTrip(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprintf("audit_%t", present), func(t *testing.T) {
			var audit *service.UsageAudit
			if present {
				audit = usageAuditFixture()
			}
			log := usageAuditLogFixture(audit)
			prepared := prepareUsageLogInsert(log)
			payload := prepared.args[usageAuditColumnIndex(t)]
			if present {
				want, err := json.Marshal(audit)
				require.NoError(t, err)
				require.JSONEq(t, string(want), payload.(string))
			} else {
				require.Nil(t, payload, "missing audit must bind SQL NULL, not JSON null or an empty object")
			}

			var captured []string
			db, mock := newSQLCapturingMock(t, &captured)
			repo := &usageLogRepository{sql: db}
			mock.ExpectQuery("INSERT INTO usage_logs").WithArgs(anySliceToDriverValues(prepared.args)...).
				WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(42), log.CreatedAt))
			inserted, err := repo.Create(context.Background(), log)
			require.NoError(t, err)
			require.True(t, inserted)
			requireStaticInsertMatchesArgTypes(t, captured[0])

			mock.ExpectQuery("SELECT").WithArgs(int64(42)).WillReturnRows(
				sqlmock.NewRows(strings.Split(usageLogSelectColumns, ", ")).AddRow(usageAuditSQLRow(t, 42, log, payload)...))
			got, err := repo.GetByID(context.Background(), 42)
			require.NoError(t, err)
			require.Equal(t, "SELECT "+usageLogSelectColumns+" FROM usage_logs WHERE id = $1", captured[1])
			require.Equal(t, audit, got.UsageAudit)
			// Continue the actual repository read through the HTTP DTO boundary.
			adminJSON, err := json.Marshal(dto.UsageLogFromServiceAdmin(got))
			require.NoError(t, err)
			var adminRoundTrip dto.AdminUsageLog
			require.NoError(t, json.Unmarshal(adminJSON, &adminRoundTrip))
			require.Equal(t, audit, adminRoundTrip.UsageAudit)
			userJSON, err := json.Marshal(dto.UsageLogFromService(got))
			require.NoError(t, err)
			require.NotContains(t, string(userJSON), "usage_audit")
			require.Equal(t, log.AccountStatsCost, got.AccountStatsCost)
			require.Equal(t, log.UpstreamRequestID, got.UpstreamRequestID)
			require.Equal(t, log.SessionID, got.SessionID)
			require.Equal(t, log.CreatedAt, got.CreatedAt)
			require.Equal(t, log.CacheCreation12hTokens, got.CacheCreation12hTokens)

			mock.ExpectExec("INSERT INTO usage_logs").WithArgs(anySliceToDriverValues(prepared.args)...).
				WillReturnResult(sqlmock.NewResult(0, 1))
			require.NoError(t, execUsageLogInsertNoResult(context.Background(), db, prepared))
			requireStaticInsertMatchesArgTypes(t, captured[2])
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageAuditBatchInsertShapes(t *testing.T) {
	cols := strings.Split(usageLogSelectColumns, ", ")[1:]
	cteRE := regexp.MustCompile(`(?s)WITH input \((.*?)\) AS \(VALUES`)
	insertRE := regexp.MustCompile(`(?s)INSERT INTO usage_logs \((.*?)\)\s+SELECT`)
	selectRE := regexp.MustCompile(`(?s)\)\s+SELECT\s+(.*?)\s+FROM input`)
	split := func(value string) []string {
		out := strings.Split(value, ",")
		for i := range out {
			out[i] = strings.TrimSpace(out[i])
		}
		return out
	}
	for _, mask := range [][]bool{{false}, {true}, {false, false}, {false, true, false}, {true, false, true}} {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			var keys []string
			var list []usageLogInsertPrepared
			byKey := map[string]usageLogInsertPrepared{}
			for i, present := range mask {
				var audit *service.UsageAudit
				if present {
					audit = usageAuditFixture()
				}
				log := usageAuditLogFixture(audit)
				log.RequestID = fmt.Sprintf("batch-%d", i)
				prepared := prepareUsageLogInsert(log)
				require.Len(t, prepared.args, len(usageLogInsertArgTypes))
				key := usageLogBatchKey(log.RequestID, log.APIKeyID)
				keys, list = append(keys, key), append(list, prepared)
				byKey[key] = prepared
			}
			for _, indexed := range []bool{false, true} {
				t.Run(fmt.Sprintf("indexed_%t", indexed), func(t *testing.T) {
					query, args := buildUsageLogBestEffortInsertQuery(list)
					inputCols, stride := cols, len(cols)
					if indexed {
						query, args = buildUsageLogBatchInsertQuery(keys, byKey)
						inputCols, stride = append([]string{"input_idx"}, cols...), len(cols)+1
					}
					for _, check := range []struct {
						re   *regexp.Regexp
						want []string
					}{
						{cteRE, inputCols}, {insertRE, cols}, {selectRE, cols},
					} {
						match := check.re.FindStringSubmatch(query)
						require.Len(t, match, 2, query)
						require.Equal(t, check.want, split(match[1]))
					}
					require.Len(t, args, len(mask)*stride)
					placeholders := usageLogPlaceholderRe.FindAllStringSubmatch(query, -1)
					require.Len(t, placeholders, len(args))
					for i, placeholder := range placeholders {
						require.Equal(t, strconv.Itoa(i+1), placeholder[1])
					}
					for row, prepared := range list {
						start := row * stride
						if indexed {
							require.Equal(t, row, args[start])
							start++
						}
						require.Equal(t, prepared.args, args[start:start+len(cols)])
						for i, argType := range usageLogInsertArgTypes {
							require.Contains(t, query, fmt.Sprintf("$%d::%s", start+i+1, argType))
						}
						arg := args[start+usageAuditColumnIndex(t)]
						if mask[row] {
							require.IsType(t, "", arg)
						} else {
							require.Nil(t, arg)
						}
					}
				})
			}
		})
	}
}

func TestUsageAuditListMalformedAndNullDoNotAbortPage(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	log := usageAuditLogFixture(usageAuditFixture())
	payload := prepareUsageLogInsert(log).args[usageAuditColumnIndex(t)].(string)
	values := []any{payload, nil, "null", `{"status":`, []byte(payload),
		`{"status":"pass","reasons":["private-request-body"],"gemini_usage":{"frames":"private-response-body"}}`, "", payload}
	rows := sqlmock.NewRows(strings.Split(usageLogSelectColumns, ", "))
	for i, value := range values {
		rows.AddRow(usageAuditSQLRow(t, int64(i+1), log, value)...)
	}

	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	query := "SELECT " + usageLogSelectColumns + " FROM usage_logs  ORDER BY id DESC LIMIT $1 OFFSET $2"
	mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(21, 0).WillReturnRows(rows)
	got, page, err := repo.listUsageLogsWithFastPagination(context.Background(), "", nil, pagination.PaginationParams{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, got, len(values))
	require.EqualValues(t, len(values), page.Total)
	for i := range got {
		if i == 0 || i == 4 || i == 7 {
			require.Equal(t, log.UsageAudit, got[i].UsageAudit)
		} else {
			require.Nil(t, got[i].UsageAudit)
		}
		require.Equal(t, log.InputTokens, got[i].InputTokens)
		require.Equal(t, log.SessionID, got[i].SessionID)
	}
	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, 3, strings.Count(logs.String(), "cannot decode usage_audit"))
	for _, id := range []int{4, 6, 7} {
		require.Contains(t, logs.String(), fmt.Sprintf(`"usage_log_id":%d`, id))
	}
	require.Contains(t, logs.String(), "error_type")
	require.NotContains(t, logs.String(), "private-request-body")
	require.NotContains(t, logs.String(), "private-response-body")
}

func TestUsageAuditInsertInvalidEvidenceIsObservableAndSnapshotIsStable(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	for _, audit := range []*service.UsageAudit{
		{UpstreamRawUsage: json.RawMessage(`{"private-request-body":`)},
		{CacheHitRate: func() *float64 { value := math.NaN(); return &value }()},
	} {
		prepared := prepareUsageLogInsert(usageAuditLogFixture(audit))
		require.Nil(t, prepared.args[usageAuditColumnIndex(t)])
		require.Len(t, prepared.args, len(usageLogInsertArgTypes))
	}
	require.Equal(t, 2, strings.Count(logs.String(), "cannot encode usage_audit"))
	require.NotContains(t, logs.String(), "private-request-body")
	audit := usageAuditFixture()
	prepared := prepareUsageLogInsert(usageAuditLogFixture(audit))
	payload := prepared.args[usageAuditColumnIndex(t)].(string)
	audit.Reasons[0] = "changed-after-queue"
	require.NotContains(t, payload, "changed-after-queue")
}
