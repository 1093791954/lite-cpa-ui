package reqlog_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Mieluoxxx/lite-cpa/internal/reqlog"
	_ "modernc.org/sqlite"
)

func TestOpenMigratesCachedTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "requests.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE request_logs (
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
    resp_body TEXT NOT NULL DEFAULT '',
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0
  );`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := reqlog.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	db2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	rows, err := db2.Query(`PRAGMA table_info(request_logs)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	if !cols["cached_tokens"] {
		t.Fatalf("missing cached_tokens: %#v", cols)
	}
	st, err := store.Stats(context.Background(), reqlog.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	_ = st
}
