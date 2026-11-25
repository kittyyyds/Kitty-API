package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const kittyAPIKeyDigestPrefix = "$sha256$"

// KittyAPIKeyDigest computes the lowercase hex SHA-256 digest of a raw API key.
// Matches 900_kitty_api_key_hash.sql (plain SHA-256, UTF-8, lowercase hex).
func KittyAPIKeyDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// KittyAPIKeyStoredValue formats a raw key as "$sha256$<sha256_hex>$<display_mask>".
func KittyAPIKeyStoredValue(raw string) string {
	return kittyAPIKeyDigestPrefix + KittyAPIKeyDigest(raw) + "$" + MaskKittyAPIKey(raw)
}

// KittyAPIKeyStoredDigest extracts the 64-char hex digest from a "$sha256$<hex>$..." stored key,
// or hashes a plaintext stored key. Exact Go equivalent of SQL kitty_api_key_cache_digest().
func KittyAPIKeyStoredDigest(stored string) string {
	if isKittyDigestStoredKey(stored) {
		return stored[len(kittyAPIKeyDigestPrefix) : len(kittyAPIKeyDigestPrefix)+64]
	}
	return KittyAPIKeyDigest(stored)
}

// IsKittyDigestStoredKey reports whether a stored api_keys.key value uses Kitty's irreversible "$sha256$<64hex>$<mask>" format.
func IsKittyDigestStoredKey(stored string) bool {
	return isKittyDigestStoredKey(stored)
}

// KittyAPIKeyPrefix returns a short safe prefix for display metadata.
func KittyAPIKeyPrefix(key string) string {
	if isKittyDigestStoredKey(key) {
		mask := key[len(kittyAPIKeyDigestPrefix)+65:]
		if idx := strings.Index(mask, "..."); idx > 0 {
			return mask[:idx]
		}
		if idx := strings.Index(mask, "*"); idx > 0 {
			return mask[:idx]
		}
		return mask
	}
	chars := []rune(key)
	if len(chars) > 12 {
		return string(chars[:12])
	}
	return key
}

func isKittyDigestStoredKey(stored string) bool {
	if !strings.HasPrefix(stored, kittyAPIKeyDigestPrefix) || len(stored) < len(kittyAPIKeyDigestPrefix)+65 {
		return false
	}
	if stored[len(kittyAPIKeyDigestPrefix)+64] != '$' {
		return false
	}
	hexPart := stored[len(kittyAPIKeyDigestPrefix) : len(kittyAPIKeyDigestPrefix)+64]
	for i := 0; i < len(hexPart); i++ {
		c := hexPart[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// MaskKittyAPIKey returns a safe display mask for either a stored "$sha256$" key or a raw key.
func MaskKittyAPIKey(key string) string {
	if isKittyDigestStoredKey(key) {
		return key[len(kittyAPIKeyDigestPrefix)+65:]
	}
	chars := []rune(key)
	if len(chars) <= 12 {
		return "***"
	}
	return string(chars[:6]) + "..." + string(chars[len(chars)-4:])
}

// kittyAuthInvalidationCacheKey resolves the auth cache key for any stored DB key
// (both migrated "$sha256$" rows and plaintext CS rows) so invalidation matches authCacheKey(raw).
func (s *APIKeyService) kittyAuthInvalidationCacheKey(stored string) string {
	return KittyAPIKeyStoredDigest(stored)
}

type kittyAPIKeySecretRepo interface {
	KittyGetByIDForAuth(ctx context.Context, id int64) (*APIKey, error)
}

// KittyGetForOwnerAuth loads an API key by ID for JWT-authenticated panel operations.
func (s *APIKeyService) KittyGetForOwnerAuth(ctx context.Context, id, userID int64) (*APIKey, error) {
	repo, ok := s.apiKeyRepo.(kittyAPIKeySecretRepo)
	if !ok {
		return nil, ErrAPIKeyNotFound
	}
	key, err := repo.KittyGetByIDForAuth(ctx, id)
	if err != nil {
		return nil, err
	}
	if key == nil || key.UserID != userID {
		return nil, ErrAPIKeyNotFound
	}
	s.compileAPIKeyIPRules(key)
	return key, nil
}
