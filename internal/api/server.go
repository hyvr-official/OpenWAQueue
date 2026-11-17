package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"openwaqueue/internal/store"
)

const maxBodyBytes = 32 << 20

type Server struct {
	store *store.Store
}

func New(s *store.Store) http.Handler {
	srv := &Server{store: s}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", srv.health)
	mux.HandleFunc("/", srv.enqueue)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) enqueue(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"queued": false, "error": "request body too large"})
		return
	}

	path := strings.Trim(r.URL.Path, "/")
	path = strings.TrimPrefix(path, "api/")
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"queued": false, "error": "path is required"})
		return
	}

	id, err := s.store.Enqueue(r.Method, path, r.URL.RawQuery, string(body))
	if err != nil {
		log.Printf("enqueue failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"queued": false, "error": "could not queue request"})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "id": id})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
