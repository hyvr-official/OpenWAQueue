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

	"openwaqueue/internal/admin"
	"openwaqueue/internal/api"
	"openwaqueue/internal/config"
	"openwaqueue/internal/store"
	"openwaqueue/internal/version"
	"openwaqueue/internal/worker"
)

func main() {
	log.Printf("%s v%s", version.Name, version.Version)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	queueKey, err := db.EnsureQueueAPIKey()
	if err != nil {
		log.Fatalf("queue api key: %v", err)
	}
	log.Printf("Queue API key (send as X-Queue-API-Key header): %s", queueKey)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	apiSrv := &http.Server{Addr: ":" + cfg.APIPort, Handler: api.New(db), ReadHeaderTimeout: 10 * time.Second}
	adminSrv := &http.Server{Addr: ":" + cfg.AdminPort, Handler: admin.New(db, cfg), ReadHeaderTimeout: 10 * time.Second}

	for _, srv := range []*http.Server{apiSrv, adminSrv} {
		go func(s *http.Server) {
			log.Printf("listening on %s", s.Addr)
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("server %s: %v", s.Addr, err)
			}
		}(srv)
	}

	done := make(chan struct{})
	go func() {
		worker.New(db, cfg).Run(ctx)
		close(done)
	}()

	<-ctx.Done()
	log.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = apiSrv.Shutdown(shutdownCtx)
	_ = adminSrv.Shutdown(shutdownCtx)
	<-done
}
