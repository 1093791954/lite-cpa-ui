package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mieluoxxx/lite-cpa/internal/config"
	"github.com/Mieluoxxx/lite-cpa/internal/reqlog"
	"github.com/Mieluoxxx/lite-cpa/internal/server"
	"github.com/Mieluoxxx/lite-cpa/internal/translator"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.Debug {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	translator.RegisterBuiltin()

	logger, err := reqlog.Open(cfg.RequestLog)
	if err != nil {
		log.Fatalf("request-log: %v", err)
	}
	defer func() {
		if err := logger.Close(); err != nil {
			log.Printf("request-log close: %v", err)
		}
	}()
	if cfg.RequestLog.Enabled {
		log.Printf("request-log enabled backend=%s retention=%s store-body=%v",
			cfg.RequestLog.Backend, cfg.RequestLog.Retention, cfg.RequestLog.StoreBody)
	}

	srv := server.New(cfg, logger)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("server: %v", err)
		}
	case sig := <-sigCh:
		log.Printf("signal %v, shutting down", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
}
