package reqlog

import (
	"context"
	"time"
)

// Record is a single API request metadata row.
// Bodies are optional and only stored when store-body is enabled.
type Record struct {
	RequestID  string
	Timestamp  time.Time
	Method     string
	Path       string
	StatusCode int
	Model      string
	Protocol   string // chat | responses | claude
	Provider   string // upstream provider type
	Upstream   string // provider profile name
	UserAgent  string
	DurationMS int64
	Error      string
	ReqBody    string
	RespBody   string
}

// Store persists request records and supports retention cleanup.
type Store interface {
	Insert(ctx context.Context, r Record) error
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	Close() error
}

// Noop is used when request logging is disabled.
type Noop struct{}

func (Noop) Insert(context.Context, Record) error                   { return nil }
func (Noop) DeleteOlderThan(context.Context, time.Time) (int64, error) { return 0, nil }
func (Noop) Close() error                                           { return nil }
