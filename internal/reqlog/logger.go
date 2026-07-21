package reqlog

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Mieluoxxx/lite-cpa/internal/config"
)

// Logger wraps a Store with async insert and retention cleanup.
type Logger struct {
	store     Store
	storeBody bool
	retention time.Duration

	ch     chan Record
	done   chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func Open(cfg config.RequestLogConfig) (*Logger, error) {
	if !cfg.Enabled {
		// Disabled: no worker, no channel. Enabled() is false.
		return &Logger{store: Noop{}}, nil
	}
	retention, err := time.ParseDuration(cfg.Retention)
	if err != nil {
		return nil, err
	}
	var store Store
	switch strings.ToLower(strings.TrimSpace(cfg.Backend)) {
	case "postgres", "postgresql", "pg":
		store, err = OpenPostgres(cfg.Postgres.DSN)
	default:
		store, err = OpenSQLite(cfg.SQLite.Path)
	}
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	l := &Logger{
		store:     store,
		storeBody: cfg.StoreBody,
		retention: retention,
		ch:        make(chan Record, 256),
		done:      make(chan struct{}),
		cancel:    cancel,
	}
	l.wg.Add(1)
	go l.loop(ctx)
	return l, nil
}

func (l *Logger) Enabled() bool {
	return l != nil && l.ch != nil
}

// Record enqueues a request log asynchronously. Never blocks the request path long:
// if the queue is full the record is dropped.
func (l *Logger) Record(r Record) {
	if l == nil || l.ch == nil {
		return
	}
	if !l.storeBody {
		r.ReqBody = ""
		r.RespBody = ""
	}
	select {
	case l.ch <- r:
	default:
		// drop under pressure
	}
}

// Close cancels the worker first, waits briefly, then closes the store.
// Guarantees the worker is not inserting after the DB is closed.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	if l.cancel != nil {
		l.cancel()
	}
	if l.ch != nil {
		// Closing the channel is optional once cancelled; avoid double-close races by
		// only closing if worker still running. Use recover-safe close.
		func() {
			defer func() { _ = recover() }()
			close(l.ch)
		}()
	}
	// Bound wait for Docker SIGTERM friendliness.
	waitDone := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		log.Printf("request-log: close timed out waiting for worker")
	}
	if l.store != nil {
		return l.store.Close()
	}
	return nil
}

func (l *Logger) loop(ctx context.Context) {
	defer l.wg.Done()
	defer close(l.done)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	// Initial cleanup shortly after start.
	if ctx.Err() == nil {
		l.cleanup(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			// Drop any remaining buffered records; do not insert after cancel.
			for {
				select {
				case _, ok := <-l.ch:
					if !ok {
						return
					}
					// discard
				default:
					return
				}
			}
		case r, ok := <-l.ch:
			if !ok {
				return
			}
			// Re-check cancel before insert so Close cannot race with DB Close.
			if ctx.Err() != nil {
				return
			}
			l.insert(ctx, r)
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			l.cleanup(ctx)
		}
	}
}

func (l *Logger) insert(parent context.Context, r Record) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if err := l.store.Insert(ctx, r); err != nil && parent.Err() == nil {
		log.Printf("request-log insert: %v", err)
	}
}

func (l *Logger) cleanup(parent context.Context) {
	if l.retention <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	n, err := l.store.DeleteOlderThan(ctx, time.Now().Add(-l.retention))
	if err != nil {
		if parent.Err() == nil {
			log.Printf("request-log retention: %v", err)
		}
		return
	}
	if n > 0 {
		log.Printf("request-log retention: deleted %d rows older than %s", n, l.retention)
	}
}
