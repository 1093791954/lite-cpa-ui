package reqlog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	// Create the current schema if the table is missing.
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS request_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT NOT NULL DEFAULT '',
  ts INTEGER NOT NULL,
  method TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  status_code INTEGER NOT NULL DEFAULT 0,
  model TEXT NOT NULL DEFAULT '',
  protocol TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '',
  upstream TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  req_body TEXT NOT NULL DEFAULT '',
  resp_body TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_request_logs_ts ON request_logs(ts);
`); err != nil {
		return err
	}
	return s.migrateTsToIntegerIfNeeded()
}

// migrateTsToIntegerIfNeeded rewrites legacy TEXT RFC3339Nano ts columns to
// INTEGER Unix nanoseconds so retention comparisons are correct.
func (s *SQLiteStore) migrateTsToIntegerIfNeeded() error {
	decl, err := s.tsColumnType()
	if err != nil {
		return err
	}
	if decl == "" {
		return nil
	}
	// Already integer (or numeric affinity).
	if strings.EqualFold(decl, "INTEGER") || strings.EqualFold(decl, "INT") || strings.EqualFold(decl, "BIGINT") {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
CREATE TABLE request_logs_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT NOT NULL DEFAULT '',
  ts INTEGER NOT NULL,
  method TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  status_code INTEGER NOT NULL DEFAULT 0,
  model TEXT NOT NULL DEFAULT '',
  protocol TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '',
  upstream TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  req_body TEXT NOT NULL DEFAULT '',
  resp_body TEXT NOT NULL DEFAULT ''
);`); err != nil {
		return fmt.Errorf("create request_logs_new: %w", err)
	}

	rows, err := tx.Query(`SELECT id, request_id, ts, method, path, status_code, model, protocol, provider, upstream, user_agent, duration_ms, error, req_body, resp_body FROM request_logs`)
	if err != nil {
		return fmt.Errorf("select legacy rows: %w", err)
	}
	defer rows.Close()

	ins, err := tx.Prepare(`INSERT INTO request_logs_new (
  id, request_id, ts, method, path, status_code, model, protocol, provider, upstream,
  user_agent, duration_ms, error, req_body, resp_body
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()

	for rows.Next() {
		var (
			id, status, duration                                                   int64
			requestID, tsRaw, method, path, model, protocol, provider, upstream   string
			userAgent, errText, reqBody, respBody                                 string
		)
		if err := rows.Scan(&id, &requestID, &tsRaw, &method, &path, &status, &model, &protocol, &provider, &upstream, &userAgent, &duration, &errText, &reqBody, &respBody); err != nil {
			return fmt.Errorf("scan legacy row: %w", err)
		}
		tsNano, err := parseLegacyTS(tsRaw)
		if err != nil {
			// Skip unparsable timestamps rather than failing the whole open.
			continue
		}
		if _, err := ins.Exec(id, requestID, tsNano, method, path, status, model, protocol, provider, upstream, userAgent, duration, errText, reqBody, respBody); err != nil {
			return fmt.Errorf("insert migrated row: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if _, err := tx.Exec(`DROP TABLE request_logs`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE request_logs_new RENAME TO request_logs`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_request_logs_ts ON request_logs(ts)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) tsColumnType() (string, error) {
	rows, err := s.db.Query(`PRAGMA table_info(request_logs)`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return "", err
		}
		if name == "ts" {
			return strings.TrimSpace(ctype), nil
		}
	}
	return "", rows.Err()
}

func parseLegacyTS(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty ts")
	}
	// Already numeric (defensive).
	if isAllDigits(raw) {
		var n int64
		_, err := fmt.Sscan(raw, &n)
		return n, err
	}
	// Try common RFC3339 variants.
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC().UnixNano(), nil
		}
	}
	return 0, fmt.Errorf("unrecognized ts %q", raw)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *SQLiteStore) Insert(ctx context.Context, r Record) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO request_logs (
  request_id, ts, method, path, status_code, model, protocol, provider, upstream,
  user_agent, duration_ms, error, req_body, resp_body
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RequestID,
		r.Timestamp.UTC().UnixNano(),
		r.Method,
		r.Path,
		r.StatusCode,
		r.Model,
		r.Protocol,
		r.Provider,
		r.Upstream,
		r.UserAgent,
		r.DurationMS,
		r.Error,
		r.ReqBody,
		r.RespBody,
	)
	return err
}

func (s *SQLiteStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM request_logs WHERE ts < ?`, cutoff.UTC().UnixNano())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
