package proxy

import (
	"io"
	"net/http"

	"glm-proxy/internal/storage"
)

// relayResponse forwards a non-streaming response, then tracks tokens synchronously.
func relayResponse(w http.ResponseWriter, resp *http.Response, store *storage.KeyStore, keyValue string, tokenExtractor func([]byte) int) {
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

	// Track tokens synchronously to ensure no data loss on shutdown/restart.
	// Extract tokens regardless of status code — some upstream errors still
	// include usage data, and we always want to record the request happened.
	tokens := tokenExtractor(bodyBytes)
	store.UpdateUsage(keyValue, tokens)
}
