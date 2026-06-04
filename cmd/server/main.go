// Command server starts the RAG chatbot: it loads config, builds the
// embedder/LLM/vector store, registers plugins and skills per the config, and
// serves the HTTP API + web console.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ragbot/internal/config"
	"ragbot/internal/embedding"
	"ragbot/internal/llm"
	"ragbot/internal/middleware"
	"ragbot/internal/plugin"
	"ragbot/internal/rag"
	"ragbot/internal/server"
	"ragbot/internal/session"
	"ragbot/internal/skill"
	"ragbot/internal/vectorstore"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to config file")
	env := flag.String("env", "", "environment name (loads config.{env}.json, overrides -config)")
	watch := flag.Bool("watch", false, "enable config hot-reload (poll mtime)")
	flag.Parse()

	// Resolve config path: -env takes priority.
	if *env != "" {
		ext := filepath.Ext(*cfgPath)
		base := (*cfgPath)[:len(*cfgPath)-len(ext)]
		*cfgPath = fmt.Sprintf("%s.%s%s", base, *env, ext)
	}

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
	baseStore, err := vectorstore.NewMemory(cfg.RAG.StorePath)
	if err != nil {
		log.Fatalf("vectorstore: %v", err)
	}
	// Wrap with tenant isolation — all chunks are namespaced by tenant ID.
	store := vectorstore.NewTenantStore(baseStore)

	// ---- plugins ----
	pm := plugin.NewManager()
	pm.Register(plugin.NewTimePlugin(config.Enabled(cfg.Plugins.Enabled, "time")))
	pm.Register(plugin.NewCalculatorPlugin(config.Enabled(cfg.Plugins.Enabled, "calculator")))
	pm.Register(plugin.NewWebSearchPlugin(
		config.Enabled(cfg.Plugins.Enabled, "websearch"),
		cfg.Plugins.WebSearch.Provider,
		cfg.Plugins.WebSearch.APIKey,
		cfg.Plugins.WebSearch.Endpoint,
	))

	// ---- skills ----
	sm := skill.NewManager()
	if config.Enabled(cfg.Skills.Enabled, "email") {
		sm.Register(skill.NewEmailSkill(cfg.Skills.Email))
	}
	if config.Enabled(cfg.Skills.Enabled, "weather") {
		sm.Register(skill.NewWeatherSkill(cfg.Skills.Weather))
	}

	sessions := session.NewStore()
	engine := rag.New(cfg.RAG, emb, store, model, pm, sm, sessions)

	jwtTTL, err := parseDurationOrZero(cfg.Server.JWTTTL)
	if err != nil {
		log.Fatalf("server.jwt_ttl: %v", err)
	}

	// ---- server with full config ----
	srv := server.NewWithConfig(engine, server.ServerConfig{
		APIKey:        cfg.Server.APIKey,
		JWTSecret:     cfg.Server.JWTSecret,
		JWTTTL:        jwtTTL,
		AdminUser:     cfg.Server.AdminUsername,
		AdminPassword: cfg.Server.AdminPassword,
		CORS:          serverCORSFromEnv(),
		RateLimitRPS:  10,
		RateBurst:     30,
		AuditLogPath:  cfg.RAG.StorePath + ".audit.jsonl",
	})

	httpSrv := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      srv.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

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

	// Config hot-reload (optional, via -watch flag).
	var watcher *config.Watcher
	if *watch {
		watcher = config.NewWatcher(*cfgPath, 5*time.Second, func(newCfg *config.Config) {
			log.Printf("config: reloaded (llm=%s embed=%s)", newCfg.LLM.Provider, newCfg.Embedding.Provider)
		})
		watcher.Start()
	}

	go func() {
		log.Printf("embedder=%s  llm=%s  chunks=%d  env=%s", emb.Name(), model.Name(), store.Count(), envOrDefault(*env))
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	close(cleanupDone)

	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	// Persist vector store and close audit log.
	if err := store.Save(); err != nil {
		log.Printf("vectorstore save: %v", err)
	}
	if err := srv.Audit().Close(); err != nil {
		log.Printf("audit close: %v", err)
	}
	if watcher != nil {
		watcher.Stop()
	}

	log.Println("server stopped")
}

func envOrDefault(e string) string {
	if e == "" {
		return "default"
	}
	return e
}

func parseDurationOrZero(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return time.ParseDuration(raw)
}

// serverCORSFromEnv reads CORS configuration from environment variables.
// Set CORS_ALLOWED_ORIGINS to restrict allowed origins (comma-separated),
// or leave empty for dev-friendly allow-all.
func serverCORSFromEnv() middleware.CORSConfig {
	cfg := middleware.DefaultCORS()
	if origins := os.Getenv("CORS_ALLOWED_ORIGINS"); origins != "" {
		cfg.AllowedOrigins = strings.Split(origins, ",")
		for i := range cfg.AllowedOrigins {
			cfg.AllowedOrigins[i] = strings.TrimSpace(cfg.AllowedOrigins[i])
		}
	}
	return cfg
}
