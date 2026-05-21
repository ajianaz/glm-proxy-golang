package proxy

import (
	"io"
	"net/http"

	"glm-proxy/internal/storage"
)

// relayResponse forwards a non-streaming response, then tracks tokens and cost synchronously.
func relayResponse(w http.ResponseWriter, resp *http.Response, store *storage.KeyStore, keyValue string, tokenExtractor func([]byte) TokenResult) {
	// Extract cost from upstream response headers (LiteLLM)
	cost := parseCostFromHeader(resp.Header)

	// Copy response headers
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}

	// Read entire body
	bodyBytes, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		WriteError(w, http.StatusBadGateway, "Failed to read upstream response")
		return
	}

	w.WriteHeader(resp.StatusCode)
	w.Write(bodyBytes)

	// Track tokens and cost synchronously to ensure no data loss on shutdown/restart.
	result := tokenExtractor(bodyBytes)
	store.UpdateUsage(keyValue, result.Total, result.Cached, cost)
}
