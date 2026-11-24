//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKitty36ChannelUltrafastPersistenceRead(t *testing.T) {
	for _, value := range []any{nil, 3.0, 4.25} {
		repo, mock := newChannelModelPricingTimePricingRepo(t)
		mock.ExpectQuery(`(?s)SELECT .*ultrafast_multiplier.*FROM channel_model_pricing.*channel_id = \$1`).WithArgs(int64(7)).WillReturnRows(modelPricingTimePricingRow(nil, value))
		expectEmptyModelPricingIntervals(mock)
		rows, err := repo.ListModelPricing(context.Background(), 7)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		if value == nil {
			require.Nil(t, rows[0].UltrafastMultiplier)
		} else {
			require.NotNil(t, rows[0].UltrafastMultiplier)
			require.Equal(t, value.(float64), *rows[0].UltrafastMultiplier)
		}
		require.NoError(t, mock.ExpectationsWereMet())
	}
}
