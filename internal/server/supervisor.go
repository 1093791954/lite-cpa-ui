package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Mieluoxxx/lite-cpa/internal/config"
	"github.com/Mieluoxxx/lite-cpa/internal/reqlog"
)

// Supervisor owns the currently serving gateway generation. The management
// listener is deliberately separate, so replacing this generation never
// interrupts the configuration UI or its apply request.
type Supervisor struct {
	mu       sync.RWMutex
	applyMu  sync.Mutex
	cfg      *config.Config
	server   *Server
	logger   *reqlog.Logger
	listener net.Listener
	revision string
	started  time.Time
}

// NewSupervisor binds and starts the initial gateway runtime.
func NewSupervisor(cfg *config.Config, revision string) (*Supervisor, error) {
	logger, err := reqlog.Open(cfg.RequestLog)
	if err != nil {
		return nil, fmt.Errorf("request-log: %w", err)
	}
	srv := New(cfg, logger)
	listener, err := net.Listen("tcp", srv.Addr())
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	s := &Supervisor{
		cfg: cfg, server: srv, logger: logger, listener: listener,
		revision: revision, started: time.Now(),
	}
	s.serve(srv, listener)
	return s, nil
}

func (s *Supervisor) serve(srv *Server, listener net.Listener) {
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("gateway serve: %v", err)
		}
	}()
}

// Apply validates external resources and replaces the running gateway. If the
// replacement cannot bind, the previous configuration is brought back.
func (s *Supervisor) Apply(cfg *config.Config, revision string) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	newLogger, err := reqlog.Open(cfg.RequestLog)
	if err != nil {
		return fmt.Errorf("request-log: %w", err)
	}
	newServer := New(cfg, newLogger)
	newAddr := newServer.Addr()

	s.mu.RLock()
	oldCfg, oldServer, oldLogger, oldListener := s.cfg, s.server, s.logger, s.listener
	oldAddr := oldServer.Addr()
	s.mu.RUnlock()

	// A different address can be proven usable before touching the old runtime.
	if newAddr != oldAddr {
		newListener, listenErr := net.Listen("tcp", newAddr)
		if listenErr != nil {
			_ = newLogger.Close()
			return fmt.Errorf("listen %s: %w", newAddr, listenErr)
		}
		s.serve(newServer, newListener)
		s.mu.Lock()
		s.cfg, s.server, s.logger, s.listener, s.revision, s.started = cfg, newServer, newLogger, newListener, revision, time.Now()
		s.mu.Unlock()
		s.retire(oldServer, oldLogger)
		return nil
	}

	// Reusing the same address requires closing the old listener first. The
	// rollback path recreates an equivalent runtime if the unexpected rebind
	// fails after Shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	shutdownErr := oldServer.Shutdown(ctx)
	cancel()
	_ = oldListener.Close()

	newListener, listenErr := net.Listen("tcp", newAddr)
	if listenErr != nil {
		_ = newLogger.Close()
		rollbackServer := New(oldCfg, oldLogger)
		rollbackListener, rollbackErr := net.Listen("tcp", oldAddr)
		if rollbackErr != nil {
			return fmt.Errorf("listen %s: %v; rollback failed: %w", newAddr, listenErr, rollbackErr)
		}
		s.serve(rollbackServer, rollbackListener)
		s.mu.Lock()
		s.server, s.listener = rollbackServer, rollbackListener
		s.mu.Unlock()
		return fmt.Errorf("listen %s: %w", newAddr, listenErr)
	}

	s.serve(newServer, newListener)
	s.mu.Lock()
	s.cfg, s.server, s.logger, s.listener, s.revision, s.started = cfg, newServer, newLogger, newListener, revision, time.Now()
	s.mu.Unlock()
	if shutdownErr == nil {
		_ = oldLogger.Close()
	} else {
		log.Printf("old gateway shutdown: %v (request logger retained for active requests)", shutdownErr)
	}
	return nil
}

func (s *Supervisor) retire(srv *Server, logger *reqlog.Logger) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := srv.Shutdown(ctx)
		cancel()
		if err != nil {
			log.Printf("old gateway shutdown: %v (request logger retained for active requests)", err)
			return
		}
		if err := logger.Close(); err != nil {
			log.Printf("old request-log close: %v", err)
		}
	}()
}

// Snapshot returns immutable runtime metadata and the current logger pointer.
func (s *Supervisor) Snapshot() (*config.Config, *reqlog.Logger, string, string, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, s.logger, s.server.Addr(), s.revision, s.started
}

// Shutdown stops the active gateway and closes its request logger.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.RLock()
	srv, logger := s.server, s.logger
	s.mu.RUnlock()
	err := srv.Shutdown(ctx)
	if closeErr := logger.Close(); err == nil {
		err = closeErr
	}
	return err
}
