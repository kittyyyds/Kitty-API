//go:build unit

package admin

import (
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestNormalizeInt64IDList(t *testing.T) {
	tests := []struct {
		name string
		in   []int64
		want []int64
	}{
		{"nil input", nil, nil},
		{"empty input", []int64{}, nil},
		{"single element", []int64{5}, []int64{5}},
		{"already sorted unique", []int64{1, 2, 3}, []int64{1, 2, 3}},
		{"duplicates removed", []int64{3, 1, 3, 2, 1}, []int64{1, 2, 3}},
		{"zero filtered", []int64{0, 1, 2}, []int64{1, 2}},
		{"negative filtered", []int64{-5, -1, 3}, []int64{3}},
		{"all invalid", []int64{0, -1, -2}, []int64{}},
		{"sorted output", []int64{9, 3, 7, 1}, []int64{1, 3, 7, 9}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeInt64IDList(tc.in)
			if tc.want == nil {
				require.Nil(t, got)
			} else {
				require.Equal(t, tc.want, got)
			}
		})
	}
}

func TestBuildAccountTodayStatsBatchCacheKey(t *testing.T) {
	day := time.Date(2026, 11, 26, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	prefix := "accounts_usage_summary:v1:" + strconv.FormatInt(day.Unix(), 10)
	tests := []struct {
		name   string
		ids    []int64
		suffix string
	}{
		{"empty", nil, ""},
		{"single", []int64{42}, ":42"},
		{"multiple", []int64{1, 2, 3}, ":1:2:3"},
		{"sorted and deduplicated", []int64{42, 7, 42, 7}, ":7:42"},
		{"invalid IDs filtered", []int64{0, -1, 42}, ":42"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, prefix+tc.suffix, buildAccountUsageSummaryCacheKey(tc.ids, day))
			// Exercise the public wrapper too, without making the assertion flaky if
			// the application timezone crosses midnight while the test is running.
			before := timezone.Today()
			got := buildAccountTodayStatsBatchCacheKey(tc.ids)
			after := timezone.Today()
			require.Contains(t, []string{
				buildAccountUsageSummaryCacheKey(tc.ids, before),
				buildAccountUsageSummaryCacheKey(tc.ids, after),
			}, got)
		})
	}
	t.Run("new day invalidates the snapshot", func(t *testing.T) {
		ids := []int64{42, 7, 42}
		original := append([]int64(nil), ids...)
		today := buildAccountUsageSummaryCacheKey(ids, day)
		tomorrow := buildAccountUsageSummaryCacheKey(ids, day.AddDate(0, 0, 1))
		require.NotEqual(t, today, tomorrow)
		require.Equal(t, original, ids, "cache normalization must not mutate the caller's IDs")
	})
}
