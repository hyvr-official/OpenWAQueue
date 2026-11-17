package admin

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"openwaqueue/internal/config"
	"openwaqueue/internal/store"
	"openwaqueue/internal/version"
)

//go:embed web
var webFS embed.FS

const (
	cookieName = "openwaqueue_session"
	pageSize   = 20
	sessionTTL = 24 * time.Hour
)

type Server struct {
	store *store.Store
	cfg   *config.Config
	web   fs.FS
}

func New(s *store.Store, cfg *config.Config) http.Handler {
	sub, _ := fs.Sub(webFS, "web")
	static, _ := fs.Sub(sub, "static")
	srv := &Server{store: s, cfg: cfg, web: sub}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", srv.loginPage)
	mux.HandleFunc("POST /login", srv.loginSubmit)
	mux.HandleFunc("POST /logout", srv.logout)
	mux.HandleFunc("GET /{$}", srv.auth(srv.indexPage))
	mux.HandleFunc("GET /settings", srv.auth(srv.settingsPage))
	mux.HandleFunc("GET /setup", srv.setupPage)
	mux.HandleFunc("POST /admin/setup", srv.saveSetup)
	mux.Handle("GET /static/", http.StripPrefix("/static/", noStore(http.FileServer(http.FS(static)))))
	mux.HandleFunc("GET /admin/settings", srv.auth(srv.getSettings))
	mux.HandleFunc("POST /admin/settings", srv.auth(srv.saveSettings))
	mux.HandleFunc("POST /admin/settings/regenerate-key", srv.auth(srv.regenerateQueueKey))
	mux.HandleFunc("GET /admin/queue", srv.auth(srv.queue))
	mux.HandleFunc("POST /admin/queue/clear", srv.auth(srv.clearQueue))
	mux.HandleFunc("POST /admin/queue/retry-failed", srv.auth(srv.retryFailed))
	mux.HandleFunc("POST /admin/queue/{id}/retry", srv.auth(srv.retryItem))
	mux.HandleFunc("DELETE /admin/queue/{id}", srv.auth(srv.deleteItem))
	mux.HandleFunc("GET /admin/state", srv.auth(srv.state))
	mux.HandleFunc("POST /admin/pause", srv.auth(srv.pause))
	mux.HandleFunc("POST /admin/resume", srv.auth(srv.resume))
	return mux
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) passwordSet() bool {
	return s.cfg.AdminPassword != "" || s.store.PasswordHash() != ""
}

func (s *Server) secretKey() []byte {
	seed := s.cfg.AdminPassword
	if seed == "" {
		seed = s.store.PasswordHash()
	}
	sum := sha256.Sum256([]byte("openwaqueue-session:" + seed))
	return sum[:]
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := sha256.Sum256(append(salt, []byte(password)...))
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(sum[:]), nil
}

func verifyPassword(stored, password string) bool {
	salt, want, ok := strings.Cut(stored, ":")
	if !ok {
		return false
	}
	saltBytes, err := hex.DecodeString(salt)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(append(saltBytes, []byte(password)...))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(want)) == 1
}

func (s *Server) checkPassword(password string) bool {
	if s.cfg.AdminPassword != "" {
		return subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.AdminPassword)) == 1
	}
	return verifyPassword(s.store.PasswordHash(), password)
}

func (s *Server) setSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.sign(time.Now().Add(sessionTTL).Unix()),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func (s *Server) sign(expires int64) string {
	payload := strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, s.secretKey())
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validToken(token string) bool {
	for i := 0; i < len(token); i++ {
		if token[i] != '.' {
			continue
		}
		expires, err := strconv.ParseInt(token[:i], 10, 64)
		if err != nil || time.Now().Unix() > expires {
			return false
		}
		return hmac.Equal([]byte(token), []byte(s.sign(expires)))
	}
	return false
}

func (s *Server) authed(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	return err == nil && s.validToken(c.Value)
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			if r.URL.Path == "/" || r.URL.Path == "/settings" || r.URL.Path == "/setup" {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) render(w http.ResponseWriter, page, active string, extra map[string]any) {
	t, err := template.ParseFS(s.web, "templates/layout.html", "templates/navbar.html", "templates/"+page+".html")
	if err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	data := map[string]any{"Active": active, "Version": version.Version}
	for k, v := range extra {
		data[k] = v
	}
	if err := t.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.authed(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if !s.passwordSet() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	s.render(w, "login", "", nil)
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.passwordSet() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}
	if !s.checkPassword(r.PostForm.Get("password")) {
		time.Sleep(500 * time.Millisecond)
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}

	s.setSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) configured() bool {
	return s.store.OpenWAConfigured(s.cfg.OpenWABaseURL)
}

func (s *Server) indexPage(w http.ResponseWriter, r *http.Request) {
	if !s.configured() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	s.render(w, "index", "queue", nil)
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "settings", "settings", nil)
}

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	needPassword := !s.passwordSet()
	needOpenWA := !s.configured()
	if !needPassword && !s.authed(r) {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if !needPassword && !needOpenWA {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	s.render(w, "setup", "", map[string]any{"NeedPassword": needPassword, "NeedOpenWA": needOpenWA})
}

func normalizeBaseURL(raw string) (string, bool) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	raw = strings.TrimRight(strings.TrimSuffix(raw, "/api"), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return raw, true
}

func (s *Server) saveOpenWA(w http.ResponseWriter, baseURL, apiKey string) bool {
	normalized, ok := normalizeBaseURL(baseURL)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid base url"})
		return false
	}
	if err := s.store.SetOpenWABaseURL(normalized); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return false
	}
	if err := s.store.SetOpenWAAPIKey(strings.TrimSpace(apiKey)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return false
	}
	return true
}

func (s *Server) saveSetup(w http.ResponseWriter, r *http.Request) {
	needPassword := !s.passwordSet()
	needOpenWA := !s.configured()
	if !needPassword && !s.authed(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if !needPassword && !needOpenWA {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "already configured"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid form"})
		return
	}

	var hash string
	if needPassword {
		password := r.PostForm.Get("password")
		if password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "password is required"})
			return
		}
		var err error
		if hash, err = hashPassword(password); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	}
	if needOpenWA {
		if _, ok := normalizeBaseURL(r.PostForm.Get("openwa_base_url")); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid base url"})
			return
		}
		if !s.saveOpenWA(w, r.PostForm.Get("openwa_base_url"), r.PostForm.Get("openwa_api_key")) {
			return
		}
	}
	if needPassword {
		if err := s.store.SetPasswordHash(hash); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		s.setSession(w)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"timezone":        s.store.Timezone(),
		"time_format":     s.store.TimeFormat(),
		"openwa_base_url": s.store.OpenWABaseURL(s.cfg.OpenWABaseURL),
		"openwa_api_key":  s.store.OpenWAAPIKey(s.cfg.OpenWAAPIKey),
		"queue_api_key":   s.store.QueueAPIKey(),
	})
}

func (s *Server) regenerateQueueKey(w http.ResponseWriter, r *http.Request) {
	key, err := s.store.RegenerateQueueAPIKey()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queue_api_key": key})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid form"})
		return
	}
	tz := r.PostForm.Get("timezone")
	if tz == "" || tz == "Local" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid timezone"})
		return
	}
	if _, err := time.LoadLocation(tz); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid timezone"})
		return
	}
	format := r.PostForm.Get("time_format")
	if format != "12" && format != "24" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid time format"})
		return
	}
	if _, ok := normalizeBaseURL(r.PostForm.Get("openwa_base_url")); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid base url"})
		return
	}
	if err := s.store.SetTimezone(tz); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if err := s.store.SetTimeFormat(format); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !s.saveOpenWA(w, r.PostForm.Get("openwa_base_url"), r.PostForm.Get("openwa_api_key")) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"timezone":        tz,
		"time_format":     format,
		"openwa_base_url": s.store.OpenWABaseURL(s.cfg.OpenWABaseURL),
		"openwa_api_key":  s.store.OpenWAAPIKey(s.cfg.OpenWAAPIKey),
	})
}

func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	items, total, err := s.store.List(page, pageSize)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	pages := (total + pageSize - 1) / pageSize
	if pages < 1 {
		pages = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"pages":     pages,
		"total":     total,
		"page_size": pageSize,
	})
}

func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	if err := s.store.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) retryItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	if err := s.store.Retry(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) retryFailed(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RetryFailed(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) clearQueue(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Clear(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.Counts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paused": s.store.IsPaused(), "counts": counts})
}

func (s *Server) pause(w http.ResponseWriter, r *http.Request) {
	s.setPaused(w, true)
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	s.setPaused(w, false)
}

func (s *Server) setPaused(w http.ResponseWriter, paused bool) {
	if err := s.store.SetPaused(paused); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paused": paused})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
