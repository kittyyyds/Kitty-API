//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseOptionalUsageTimeRangeAcceptsRFC3339AndCalendarDates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage?start_date=2026-11-24T11:20:00.000Z&end_date=2026-11-25T11:20:00.000Z", nil)
	start, end, err := parseOptionalUsageTimeRange(c)
	require.NoError(t, err)
	require.NotNil(t, start)
	require.NotNil(t, end)
	require.Equal(t, time.Date(2026, 11, 24, 11, 20, 0, 0, time.UTC), start.UTC())
	require.Equal(t, time.Date(2026, 11, 25, 11, 20, 0, 0, time.UTC), end.UTC())

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage?start_date=2026-11-24&end_date=2026-11-24&timezone=Asia/Shanghai", nil)
	start2, end2, err2 := parseOptionalUsageTimeRange(c2)
	require.NoError(t, err2)
	require.NotNil(t, start2)
	require.NotNil(t, end2)
	require.Equal(t, 24*time.Hour, end2.Sub(*start2))
}
