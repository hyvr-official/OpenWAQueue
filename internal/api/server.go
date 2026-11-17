package api

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"openwaqueue/internal/store"
)

const maxBodyBytes = 32 << 20

var allowedActions = map[string]struct{}{
	"send-text":     {},
	"send-image":    {},
	"send-video":    {},
	"send-audio":    {},
	"send-document": {},
	"send-location": {},
	"send-contact":  {},
	"send-sticker":  {},
	"send-poll":     {},
	"send-bulk":     {},
}

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
	expected := s.store.QueueAPIKey()
	provided := r.Header.Get("X-Queue-API-Key")
	if expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"queued": false, "error": "invalid or missing API key"})
		return
	}

	path := strings.Trim(r.URL.Path, "/")
	path = strings.TrimPrefix(path, "api/")
	if r.Method != http.MethodPost || !allowedPath(path) {
		writeJSON(w, http.StatusNotFound, map[string]any{"queued": false, "error": "endpoint not allowed"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"queued": false, "error": "request body too large"})
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

func allowedPath(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[0] != "sessions" || parts[1] == "" || parts[2] != "messages" {
		return false
	}
	_, ok := allowedActions[parts[3]]
	return ok
}

func writeJSON(w http.ResponseWriter, status int, v map[string]any) {
	if status >= http.StatusBadRequest {
		v["status"] = "error"
	} else {
		v["status"] = "success"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
