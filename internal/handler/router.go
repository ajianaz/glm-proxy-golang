package handler

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"glm-proxy/internal/config"
	"glm-proxy/internal/litellm"
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
	r.Get("/", Index(cfg))
	r.Get("/health", Health)

	// Protected routes (proxy users)
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(store))
		r.Use(middleware.RateLimit())
		r.Use(middleware.ModelValidate(cfg))

		r.Get("/stats", Stats(store, cfg.DefaultModel))

		// Anthropic: POST /v1/messages (must be before the catch-all)
		r.Post("/v1/messages", Anthropic(anthropicProxy))

		// OpenAI-compatible: ALL /v1/*
		r.Route("/v1", func(r chi.Router) {
			r.HandleFunc("/*", OpenAI(openaiProxy))
		})
	})

	// Admin routes (gated by master API key from env)
	llmClient := litellm.NewClient(cfg.OpenAIUpstream, cfg.MasterKey, cfg.EnvMode)
	admin := NewAdminHandler(store.DB(), llmClient)
	r.Route("/admin", func(r chi.Router) {
		r.Use(adminAuth(cfg.AdminAPIKey))

		r.Get("/stats", admin.GlobalStats)
		r.Route("/keys", func(r chi.Router) {
			r.Get("/", admin.ListKeys)
			r.Post("/", admin.CreateKey)
			r.Get("/{id}", admin.GetKey)
			r.Put("/{id}", admin.UpdateKey)
			r.Delete("/{id}", admin.DeleteKey)
			r.Post("/{id}/regenerate", admin.RegenerateKey)
		})
	})

	return r
}

// adminAuth middleware validates the master admin API key from env.
func adminAuth(masterKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if masterKey == "" {
				writeAdminError(w, http.StatusForbidden, "admin API not configured (ADMIN_API_KEY not set)")
				return
			}

			key := ""
			if auth := r.Header.Get("Authorization"); auth != "" {
				key = strings.TrimPrefix(auth, "Bearer ")
				key = strings.TrimSpace(key)
			} else if k := r.Header.Get("x-api-key"); k != "" {
				key = k
			}

			if key == "" || key != masterKey {
				writeAdminError(w, http.StatusUnauthorized, "invalid admin API key")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
