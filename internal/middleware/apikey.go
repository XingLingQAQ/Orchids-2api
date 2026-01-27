package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"orchids-api/internal/store"
)

func extractApiKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("x-api-key")); key != "" {
		return key
	}

	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}

	return ""
}

func validateApiKey(key string, store *store.Store) bool {
	hash := sha256.Sum256([]byte(key))
	hashStr := hex.EncodeToString(hash[:])

	apiKey, err := store.GetApiKeyByHash(hashStr)
	if err != nil || apiKey == nil || !apiKey.Enabled {
		return false
	}

	go store.UpdateApiKeyLastUsed(apiKey.ID)
	return true
}

func ApiKeyAuth(store *store.Store, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := extractApiKey(r)
		if key == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"type":"authentication_error","message":"missing api key"}}`))
			return
		}

		if !validateApiKey(key, store) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid api key"}}`))
			return
		}

		next(w, r)
	}
}
