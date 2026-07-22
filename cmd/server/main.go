// Command server runs the URL shortener HTTP service.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"urlshortener/internal/cache"
	"urlshortener/internal/config"
	"urlshortener/internal/httpapi"
	"urlshortener/internal/shortener"
	"urlshortener/internal/storage"
)

func main() {
	cfg := config.Load()

	store, err := openStore(cfg)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}
	defer store.Close()

	c, err := openCache(cfg)
	if err != nil {
		log.Fatalf("init cache: %v", err)
	}
	defer c.Close()

	idgen, err := buildIDGenerator(cfg, store)
	if err != nil {
		log.Fatalf("init id generator: %v", err)
	}

	svc := shortener.New(store, idgen, c, cfg.BaseURL, cfg.CacheTTL)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpapi.NewRouter(svc),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("listening on %s (base_url=%s)", srv.Addr, cfg.BaseURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Fatalf("server error: %v", err)
	case sig := <-stop:
		log.Printf("received %s, shutting down", sig)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

// openStore selects Postgres when DATABASE_URL is set, otherwise falls back to
// the in-memory store. For Postgres it retries the initial connection so the
// service tolerates the database still starting up (e.g. under docker-compose).
func openStore(cfg config.Config) (storage.Store, error) {
	if cfg.DatabaseURL == "" {
		log.Println("DATABASE_URL not set; using in-memory store (data is not persisted)")
		return storage.NewMemoryStore(), nil
	}

	ctx := context.Background()
	var lastErr error
	for i := 1; i <= 10; i++ {
		pg, err := storage.NewPostgresStore(ctx, cfg.DatabaseURL)
		if err == nil {
			if err = pg.Migrate(ctx); err != nil {
				return nil, err
			}
			log.Println("connected to postgres; migrations applied")
			return pg, nil
		}
		lastErr = err
		log.Printf("postgres not ready (attempt %d/10): %v", i, err)
		time.Sleep(2 * time.Second)
	}
	return nil, lastErr
}

// openCache returns a Redis cache when REDIS_URL is set (retrying while it
// starts), otherwise a no-op cache so the service runs without Redis.
func openCache(cfg config.Config) (cache.Cache, error) {
	if cfg.RedisURL == "" {
		log.Println("REDIS_URL not set; caching disabled")
		return cache.NewNoop(), nil
	}

	ctx := context.Background()
	var lastErr error
	for i := 1; i <= 10; i++ {
		rc, err := cache.NewRedis(ctx, cfg.RedisURL)
		if err == nil {
			log.Printf("connected to redis; read-through cache enabled (ttl=%s)", cfg.CacheTTL)
			return rc, nil
		}
		lastErr = err
		log.Printf("redis not ready (attempt %d/10): %v", i, err)
		time.Sleep(2 * time.Second)
	}
	return nil, lastErr
}

// buildIDGenerator selects the collision-free id source from config.
func buildIDGenerator(cfg config.Config, store storage.Store) (shortener.IDGenerator, error) {
	switch cfg.IDGenerator {
	case "snowflake":
		gen, err := shortener.NewSnowflake(cfg.MachineID)
		if err != nil {
			return nil, err
		}
		log.Printf("id generator: snowflake (machine_id=%d)", cfg.MachineID)
		return gen, nil
	default:
		log.Println("id generator: sequence")
		return shortener.NewSequenceGenerator(store), nil
	}
}
