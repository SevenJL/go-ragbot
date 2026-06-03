// Command server starts the RAG chatbot: it loads config, builds the
// embedder/LLM/vector store, registers plugins and skills per the config, and
// serves the HTTP API + web console.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ragbot/internal/config"
	"ragbot/internal/embedding"
	"ragbot/internal/llm"
	"ragbot/internal/plugin"
	"ragbot/internal/rag"
	"ragbot/internal/server"
	"ragbot/internal/session"
	"ragbot/internal/skill"
	"ragbot/internal/vectorstore"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	emb, err := embedding.New(cfg.Embedding)
	if err != nil {
		log.Fatalf("embedding: %v", err)
	}
	model, err := llm.New(cfg.LLM)
	if err != nil {
		log.Fatalf("llm: %v", err)
	}
	store, err := vectorstore.NewMemory(cfg.RAG.StorePath)
	if err != nil {
		log.Fatalf("vectorstore: %v", err)
	}

	// ---- plugins (loaded + enabled per config.plugins.enabled) ----
	pm := plugin.NewManager()
	pm.Register(plugin.NewTimePlugin(config.Enabled(cfg.Plugins.Enabled, "time")))
	pm.Register(plugin.NewCalculatorPlugin(config.Enabled(cfg.Plugins.Enabled, "calculator")))
	pm.Register(plugin.NewWebSearchPlugin(
		config.Enabled(cfg.Plugins.Enabled, "websearch"),
		cfg.Plugins.WebSearch.Provider,
		cfg.Plugins.WebSearch.APIKey,
		cfg.Plugins.WebSearch.Endpoint,
	))

	// ---- skills (loaded per config.skills.enabled) ----
	sm := skill.NewManager()
	if config.Enabled(cfg.Skills.Enabled, "email") {
		sm.Register(skill.NewEmailSkill(cfg.Skills.Email))
	}
	if config.Enabled(cfg.Skills.Enabled, "weather") {
		sm.Register(skill.NewWeatherSkill(cfg.Skills.Weather))
	}

	sessions := session.NewStore()
	engine := rag.New(cfg.RAG, emb, store, model, pm, sm, sessions)
	srv := server.New(engine, cfg.Server.APIKey)

	httpSrv := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      srv.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Channel to capture server errors from the listen goroutine.
	errs := make(chan error, 1)

	// Background session pruning (every 10 minutes, 1 hour idle timeout).
	cleanupDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if n := engine.PruneSessions(1 * time.Hour); n > 0 {
					log.Printf("sessions: pruned %d idle sessions", n)
				}
			case <-cleanupDone:
				return
			}
		}
	}()

	go func() {
		log.Printf("embedder=%s  llm=%s  chunks=%d", emb.Name(), model.Name(), store.Count())
		log.Printf("plugins=%v  skills=%v", cfg.Plugins.Enabled, cfg.Skills.Enabled)
		if cfg.Server.APIKey != "" {
			log.Printf("api auth=enabled")
		}
		log.Printf("listening on %s  (open http://localhost%s)", cfg.Server.Addr, cfg.Server.Addr)
		errs <- httpSrv.ListenAndServe()
	}()

	// Wait for signal or server error.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	case sig := <-quit:
		log.Printf("received signal %v, shutting down gracefully...", sig)
	}

	// Graceful shutdown with a timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	close(cleanupDone)

	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	// Persist vector store before exiting.
	if err := store.Save(); err != nil {
		log.Printf("vectorstore save: %v", err)
	}

	log.Println("server stopped")
}
