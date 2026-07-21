package reqlog

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStore struct {
	db *sql.DB
}

func OpenPostgres(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetConnMaxLifetime(time.Hour)
	s := &PostgresStore{db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS request_logs (
  id BIGSERIAL PRIMARY KEY,
  request_id TEXT NOT NULL DEFAULT '',
  ts TIMESTAMPTZ NOT NULL,
  method TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  status_code INTEGER NOT NULL DEFAULT 0,
  model TEXT NOT NULL DEFAULT '',
  protocol TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '',
  upstream TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  duration_ms BIGINT NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  req_body TEXT NOT NULL DEFAULT '',
  resp_body TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_request_logs_ts ON request_logs(ts);
`)
	return err
}

func (s *PostgresStore) Insert(ctx context.Context, r Record) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO request_logs (
  request_id, ts, method, path, status_code, model, protocol, provider, upstream,
  user_agent, duration_ms, error, req_body, resp_body
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		r.RequestID,
		r.Timestamp.UTC(),
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

func (s *PostgresStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM request_logs WHERE ts < $1`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}
