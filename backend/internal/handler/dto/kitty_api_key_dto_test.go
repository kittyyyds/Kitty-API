//go:build unit

package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyFromServicePlaintextAndLegacyDigest(t *testing.T) {
	gid1, gid2, fb := int64(44), int64(70), int64(4)
	plain := &service.APIKey{
		ID:              1,
		UserID:          10,
		Key:             "sk-plain1234567890abcdef",
		Name:            "plain-key",
		GroupID:         &gid1,
		FallbackGroupID: &fb,
		GroupIDs:        []int64{gid1, gid2},
		Concurrency:     5,
		Status:          service.StatusActive,
	}
	dtoPlain := APIKeyFromService(plain)
	require.True(t, dtoPlain.SecretAvailable)
	require.Equal(t, "sk-plain1234567890abcdef", dtoPlain.Key)
	require.Equal(t, "sk-pla...cdef", dtoPlain.KeyDisplay)
	require.Equal(t, 5, dtoPlain.Concurrency)
	require.Equal(t, 5, dtoPlain.ConcurrencyLimit)
	require.Equal(t, []int64{44, 70}, dtoPlain.GroupIDs)
	require.NotNil(t, dtoPlain.FallbackGroupID)
	require.Equal(t, int64(4), *dtoPlain.FallbackGroupID)

	legacyStored := service.KittyAPIKeyStoredValue("sk-legacysecret9876543210")
	legacy := &service.APIKey{
		ID:     2,
		UserID: 10,
		Key:    legacyStored,
		Name:   "legacy-key",
		Status: service.StatusActive,
	}
	dtoLegacy := APIKeyFromService(legacy)
	require.False(t, dtoLegacy.SecretAvailable)
	require.NotContains(t, dtoLegacy.Key, "$sha256$")
	require.Equal(t, "sk-leg...3210", dtoLegacy.Key)
	require.Equal(t, "sk-leg...3210", dtoLegacy.KeyDisplay)
}
