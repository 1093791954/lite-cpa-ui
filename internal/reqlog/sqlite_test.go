package reqlog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSQLiteInsertAndRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "requests.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	if err := store.Insert(context.Background(), Record{
		RequestID: "r1", Timestamp: now.Add(-48 * time.Hour), Method: "POST", Path: "/v1/chat/completions",
		StatusCode: 200, Model: "m", Protocol: "chat", DurationMS: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(context.Background(), Record{
		RequestID: "r2", Timestamp: now, Method: "POST", Path: "/v1/messages",
		StatusCode: 200, Model: "c", Protocol: "claude", DurationMS: 5,
	}); err != nil {
		t.Fatal(err)
	}
	n, err := store.DeleteOlderThan(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted %d want 1", n)
	}
}

func TestSQLiteRetentionNanoOrdering(t *testing.T) {
	// Ensure fractional-second boundaries compare correctly (INTEGER ns, not TEXT).
	dir := t.TempDir()
	store, err := OpenSQLite(filepath.Join(dir, "requests.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	base := time.Date(2026, 7, 21, 12, 0, 0, 100_000_000, time.UTC) // .1s
	later := time.Date(2026, 7, 21, 12, 0, 0, 120_000_000, time.UTC) // .12s
	// Lexical RFC3339Nano would put ".12" before ".1"; integer ns must not.
	if err := store.Insert(context.Background(), Record{
		RequestID: "old", Timestamp: base, Method: "POST", Path: "/a", StatusCode: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(context.Background(), Record{
		RequestID: "new", Timestamp: later, Method: "POST", Path: "/b", StatusCode: 200,
	}); err != nil {
		t.Fatal(err)
	}
	// cutoff between base and later
	n, err := store.DeleteOlderThan(context.Background(), base.Add(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted %d want 1 (only .1s row)", n)
	}
}

func TestSQLiteMigratesLegacyTextTS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	// Create a legacy DB with TEXT RFC3339Nano timestamps.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE request_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT NOT NULL DEFAULT '',
  ts TEXT NOT NULL,
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
		t.Fatal(err)
	}
	// Lexical trap: ".12Z" < ".1Z" as text, but .12s is newer.
	oldTS := "2026-07-21T12:00:00.1Z"
	newTS := "2026-07-21T12:00:00.12Z"
	if _, err := db.Exec(`INSERT INTO request_logs (request_id, ts, method, path, status_code) VALUES
		('old', ?, 'POST', '/old', 200),
		('new', ?, 'POST', '/new', 200)`, oldTS, newTS); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// Open through production path — should migrate TEXT → INTEGER ns.
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Cutoff between .1s and .12s.
	cutoff := time.Date(2026, 7, 21, 12, 0, 0, 110_000_000, time.UTC)
	n, err := store.DeleteOlderThan(context.Background(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("after migration deleted %d want 1 (only old .1s row)", n)
	}

	// Confirm remaining row is the newer one.
	var pathLeft string
	if err := store.db.QueryRow(`SELECT path FROM request_logs`).Scan(&pathLeft); err != nil {
		t.Fatal(err)
	}
	if pathLeft != "/new" {
		t.Fatalf("remaining path %q want /new", pathLeft)
	}
}
