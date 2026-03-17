package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"glm-proxy/internal/config"
	"glm-proxy/internal/handler"
	"glm-proxy/internal/storage"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--healthcheck" {
		// Simple health check for Docker: just exit 0
		os.Exit(0)
	}

	cfg := config.Load()

	store, err := storage.NewKeyStore(cfg.DataFile)
	if err != nil {
		log.Fatalf("Failed to load API keys from %s: %v", cfg.DataFile, err)
	}
	defer store.Close()

	router := handler.NewRouter(cfg, store)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       300 * time.Second, // Long timeout for SSE streaming
		WriteTimeout:      300 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("GLM Proxy starting on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Forced shutdown: %v", err)
	}

	log.Println("Server stopped")
}
