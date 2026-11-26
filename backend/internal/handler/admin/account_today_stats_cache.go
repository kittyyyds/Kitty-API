package admin

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

var accountTodayStatsBatchCache = newSnapshotCache(30 * time.Second)

func buildAccountTodayStatsBatchCacheKey(accountIDs []int64) string {
	return buildAccountUsageSummaryCacheKey(accountIDs, timezone.Today())
}

func buildAccountUsageSummaryCacheKey(accountIDs []int64, day time.Time) string {
	ids := append([]int64(nil), accountIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b strings.Builder
	b.Grow(len(ids)*6 + 48)
	// Include the local-day boundary, so yesterday's today counters cannot leak
	// across midnight. Version the payload separately from the old today cache.
	_, _ = b.WriteString("accounts_usage_summary:v1:")
	_, _ = b.WriteString(strconv.FormatInt(day.Unix(), 10))
	previous := int64(0)
	for _, id := range ids {
		if id <= 0 || id == previous {
			continue
		}
		previous = id
		_ = b.WriteByte(':')
		_, _ = b.WriteString(strconv.FormatInt(id, 10))
	}
	return b.String()
}
