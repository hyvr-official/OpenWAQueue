package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusDone       = "done"
	StatusFailed     = "failed"
)

type Item struct {
	ID             int64  `json:"id"`
	Path           string `json:"path"`
	Method         string `json:"method"`
	Query          string `json:"query"`
	Body           string `json:"body"`
	Status         string `json:"status"`
	ResponseStatus int    `json:"response_status"`
	ResponseBody   string `json:"response_body"`
	Error          string `json:"error"`
	CreatedAt      int64  `json:"created_at"`
	ProcessedAt    int64  `json:"processed_at"`
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL,
			method TEXT NOT NULL,
			query TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			response_status INTEGER NOT NULL DEFAULT 0,
			response_body TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			processed_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_queue_status_id ON queue (status, id)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`UPDATE queue SET status = 'pending' WHERE status = 'processing'`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Enqueue(method, path, query, body string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO queue (path, method, query, body, status, created_at) VALUES (?, ?, ?, ?, 'pending', ?)`,
		path, method, query, body, time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) NextPending() (*Item, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var it Item
	err = tx.QueryRow(
		`SELECT id, path, method, query, body FROM queue WHERE status = 'pending' ORDER BY id ASC LIMIT 1`,
	).Scan(&it.ID, &it.Path, &it.Method, &it.Query, &it.Body)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`UPDATE queue SET status = 'processing' WHERE id = ?`, it.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	it.Status = StatusProcessing
	return &it, nil
}

func (s *Store) Complete(id int64, status string, responseStatus int, responseBody, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE queue SET status = ?, response_status = ?, response_body = ?, error = ?, processed_at = ? WHERE id = ?`,
		status, responseStatus, responseBody, errMsg, time.Now().Unix(), id,
	)
	return err
}

func (s *Store) Prune(keep int) error {
	_, err := s.db.Exec(
		`DELETE FROM queue WHERE status IN ('done', 'failed') AND id NOT IN (
			SELECT id FROM queue WHERE status IN ('done', 'failed') ORDER BY processed_at DESC, id DESC LIMIT ?
		)`,
		keep,
	)
	return err
}

func (s *Store) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM queue WHERE id = ? AND status != 'processing'`, id)
	return err
}

func (s *Store) Retry(id int64) error {
	_, err := s.db.Exec(
		`UPDATE queue SET status = 'pending', response_status = 0, response_body = '', error = '', processed_at = 0 WHERE id = ? AND status = 'failed'`,
		id,
	)
	return err
}

func (s *Store) RetryFailed() error {
	_, err := s.db.Exec(
		`UPDATE queue SET status = 'pending', response_status = 0, response_body = '', error = '', processed_at = 0 WHERE status = 'failed'`,
	)
	return err
}

func (s *Store) Clear() error {
	_, err := s.db.Exec(`DELETE FROM queue WHERE status != 'processing'`)
	return err
}

func (s *Store) List(page, pageSize int) ([]Item, int, error) {
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM queue`).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(
		`SELECT id, path, method, query, body, status, response_status, response_body, error, created_at, processed_at
		FROM queue ORDER BY id DESC LIMIT ? OFFSET ?`,
		pageSize, (page-1)*pageSize,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Path, &it.Method, &it.Query, &it.Body, &it.Status,
			&it.ResponseStatus, &it.ResponseBody, &it.Error, &it.CreatedAt, &it.ProcessedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	return items, total, rows.Err()
}

func (s *Store) Counts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM queue GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

func (s *Store) GetSetting(key, def string) string {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

func (s *Store) IsPaused() bool {
	return s.GetSetting("paused", "0") == "1"
}

func (s *Store) SetPaused(paused bool) error {
	v := "0"
	if paused {
		v = "1"
	}
	return s.SetSetting("paused", v)
}

func (s *Store) Timezone() string {
	return s.GetSetting("timezone", "UTC")
}

func (s *Store) TimeFormat() string {
	if s.GetSetting("time_format", "12") == "24" {
		return "24"
	}
	return "12"
}

func (s *Store) SetTimeFormat(format string) error {
	return s.SetSetting("time_format", format)
}

func (s *Store) OpenWABaseURL(def string) string {
	v := strings.TrimSpace(s.GetSetting("openwa_base_url", ""))
	if v == "" {
		v = def
	}
	return strings.TrimRight(v, "/")
}

func (s *Store) OpenWAAPIKey(def string) string {
	if v := s.GetSetting("openwa_api_key", ""); v != "" {
		return v
	}
	return def
}

func (s *Store) OpenWAConfigured(def string) bool {
	return s.OpenWABaseURL(def) != ""
}

func (s *Store) PasswordHash() string {
	return s.GetSetting("admin_password_hash", "")
}

func (s *Store) SetPasswordHash(v string) error {
	return s.SetSetting("admin_password_hash", v)
}

func generateQueueAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "owq_" + hex.EncodeToString(b), nil
}

func (s *Store) QueueAPIKey() string {
	return s.GetSetting("queue_api_key", "")
}

func (s *Store) EnsureQueueAPIKey() (string, error) {
	if v := s.QueueAPIKey(); v != "" {
		return v, nil
	}
	return s.RegenerateQueueAPIKey()
}

func (s *Store) RegenerateQueueAPIKey() (string, error) {
	key, err := generateQueueAPIKey()
	if err != nil {
		return "", err
	}
	if err := s.SetSetting("queue_api_key", key); err != nil {
		return "", err
	}
	return key, nil
}

func (s *Store) SetOpenWABaseURL(v string) error {
	return s.SetSetting("openwa_base_url", v)
}

func (s *Store) SetOpenWAAPIKey(v string) error {
	return s.SetSetting("openwa_api_key", v)
}

func (s *Store) SetTimezone(tz string) error {
	return s.SetSetting("timezone", tz)
}
