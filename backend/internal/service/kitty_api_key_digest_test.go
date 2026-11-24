package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestKittyAPIKeyDigestAndCacheKeyParity(t *testing.T) {
	raw := "sk-kitty-secret-key-0123456789abcdef"
	sum := sha256.Sum256([]byte(raw))
	expectedHex := hex.EncodeToString(sum[:])

	require.Equal(t, expectedHex, KittyAPIKeyDigest(raw))

	stored := KittyAPIKeyStoredValue(raw)
	require.Equal(t, "$sha256$"+expectedHex+"$sk-kit...cdef", stored)
	require.Equal(t, "sk-kit...cdef", MaskKittyAPIKey(stored))

	require.Equal(t, expectedHex, KittyAPIKeyStoredDigest(stored))
	require.Equal(t, expectedHex, KittyAPIKeyStoredDigest(raw))

	cache := &authCacheStub{}
	svc := NewAPIKeyService(&authRepoStub{}, nil, nil, nil, nil, cache, &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60},
	})
	require.Equal(t, svc.authCacheKey(raw), svc.kittyAuthInvalidationCacheKey(stored))
	require.Equal(t, svc.authCacheKey(raw), svc.kittyAuthInvalidationCacheKey(raw))

	svc.InvalidateAuthCacheByKey(context.Background(), stored)
	require.Equal(t, []string{expectedHex}, cache.deleteAuthKeys)
}

func TestNormalizeAPIKeyGroupIDs(t *testing.T) {
	primary := int64(10)
	got := normalizeAPIKeyGroupIDs(&primary, []int64{20, 10, 0, -1, 30, 20})
	require.Equal(t, []int64{10, 20, 30}, got)
}
