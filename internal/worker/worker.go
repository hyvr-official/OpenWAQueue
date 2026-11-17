package worker

import (
	"context"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"openwaqueue/internal/config"
	"openwaqueue/internal/store"
)

const (
	keepProcessed    = 1000
	maxResponseBytes = 64 << 10
	idleDelay        = time.Second
	minItemDelay     = 2 * time.Second
	maxItemDelay     = 5 * time.Second
)

type Worker struct {
	store  *store.Store
	cfg    *config.Config
	client *http.Client
}

func New(s *store.Store, cfg *config.Config) *Worker {
	return &Worker{
		store:  s,
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (w *Worker) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		if w.store.IsPaused() {
			if !sleep(ctx, idleDelay) {
				return
			}
			continue
		}

		item, err := w.store.NextPending()
		if err != nil {
			log.Printf("worker: fetch failed: %v", err)
			if !sleep(ctx, idleDelay) {
				return
			}
			continue
		}
		if item == nil {
			if !sleep(ctx, idleDelay) {
				return
			}
			continue
		}

		w.process(ctx, item)

		if err := w.store.Prune(keepProcessed); err != nil {
			log.Printf("worker: prune failed: %v", err)
		}

		if !sleep(ctx, minItemDelay+time.Duration(rand.Int63n(int64(maxItemDelay-minItemDelay)))) {
			return
		}
	}
}

func (w *Worker) process(ctx context.Context, item *store.Item) {
	baseURL := w.store.OpenWABaseURL(w.cfg.OpenWABaseURL)
	if baseURL == "" {
		w.finish(item.ID, store.StatusFailed, 0, "", "OpenWA base URL is not configured")
		return
	}
	target := baseURL + "/" + item.Path
	if item.Query != "" {
		target += "?" + item.Query
	}

	var reader io.Reader
	if item.Body != "" {
		reader = strings.NewReader(item.Body)
	}

	req, err := http.NewRequestWithContext(ctx, item.Method, target, reader)
	if err != nil {
		w.finish(item.ID, store.StatusFailed, 0, "", err.Error())
		return
	}
	if item.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if apiKey := w.store.OpenWAAPIKey(w.cfg.OpenWAAPIKey); apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		w.finish(item.ID, store.StatusFailed, 0, "", err.Error())
		return
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))

	status := store.StatusDone
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status = store.StatusFailed
	}
	w.finish(item.ID, status, resp.StatusCode, string(data), "")
}

func (w *Worker) finish(id int64, status string, code int, body, errMsg string) {
	if err := w.store.Complete(id, status, code, body, errMsg); err != nil {
		log.Printf("worker: save result for %d failed: %v", id, err)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
