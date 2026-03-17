package middleware

import (
	"context"
	"net/http"

	"glm-proxy/internal/storage"
)

type contextKey string

const apiKeyKey contextKey = "apiKey"

// SetApiKey stores the API key in the request context.
func SetApiKey(r *http.Request, key *storage.ApiKey) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), apiKeyKey, key))
}

// GetApiKey retrieves the API key from the request context.
func GetApiKey(r *http.Request) *storage.ApiKey {
	v := r.Context().Value(apiKeyKey)
	if v == nil {
		return nil
	}
	return v.(*storage.ApiKey)
}
