package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"glm-proxy/internal/config"
	"glm-proxy/internal/middleware"
	"glm-proxy/internal/proxy"
	"glm-proxy/internal/storage"
)

// NewRouter creates and configures the Chi router.
func NewRouter(cfg *config.Config, store *storage.KeyStore) http.Handler {
	r := chi.NewRouter()

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization", "x-api-key"},
	}))

	openaiProxy := &proxy.OpenAIProxy{Config: cfg, Store: store}
	anthropicProxy := &proxy.AnthropicProxy{Config: cfg, Store: store}

	// Public routes
	r.Get("/", Index)
	r.Get("/health", Health)

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(store))
		r.Use(middleware.RateLimit())

		r.Get("/stats", Stats(store, cfg.DefaultModel))

		// Anthropic: POST /v1/messages (must be before the catch-all)
		r.Post("/v1/messages", Anthropic(anthropicProxy))

		// OpenAI-compatible: ALL /v1/*
		r.Route("/v1", func(r chi.Router) {
			r.HandleFunc("/*", OpenAI(openaiProxy))
		})
	})

	return r
}
