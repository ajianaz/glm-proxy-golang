package proxy

import (
	"io"
	"net/http"

	"glm-proxy/internal/storage"
)

// relayResponse forwards a non-streaming response, then tracks tokens.
func relayResponse(w http.ResponseWriter, resp *http.Response, store *storage.KeyStore, keyValue string, tokenExtractor func([]byte) int) {
	// Copy response headers
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}

	// Read entire body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "Failed to read upstream response")
		return
	}

	w.WriteHeader(resp.StatusCode)
	w.Write(bodyBytes)

	// Track tokens asynchronously (fire-and-forget, like TS version)
	if resp.StatusCode == http.StatusOK {
		go func() {
			tokens := tokenExtractor(bodyBytes)
			if tokens > 0 {
				store.UpdateUsage(keyValue, tokens)
			}
		}()
	}
}
