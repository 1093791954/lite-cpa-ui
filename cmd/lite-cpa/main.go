package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Mieluoxxx/lite-cpa/internal/config"
	"github.com/Mieluoxxx/lite-cpa/internal/server"
	"github.com/Mieluoxxx/lite-cpa/internal/translator"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	adminAddr := flag.String("admin-addr", "127.0.0.1:8318", "local management listen address (or off)")
	flag.Parse()

	cfg, configData, created, err := config.LoadOrCreate(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if created {
		log.Printf("created starter config at %s; configure providers at http://%s", *configPath, *adminAddr)
	}
	if cfg.Debug {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	translator.RegisterBuiltin()

	supervisor, err := server.NewSupervisor(cfg, server.ConfigRevision(configData))
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	if cfg.RequestLog.Enabled {
		log.Printf("request-log enabled backend=%s retention=%s store-body=%v",
			cfg.RequestLog.Backend, cfg.RequestLog.Retention, cfg.RequestLog.StoreBody)
	}

	var admin *server.AdminServer
	adminErrCh := make(chan error, 1)
	if !strings.EqualFold(strings.TrimSpace(*adminAddr), "off") {
		admin = server.NewAdminServer(*configPath, *adminAddr, supervisor)
		go func() {
			log.Printf("lite-cpa management UI listening on http://%s", *adminAddr)
			adminErrCh <- admin.ListenAndServe()
		}()
	} else {
		log.Printf("lite-cpa management UI disabled")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var runErr error
	select {
	case err := <-adminErrCh:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("management server: %v", err)
			runErr = err
		}
	case sig := <-sigCh:
		log.Printf("signal %v, shutting down", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if admin != nil {
		if err := admin.Shutdown(ctx); err != nil {
			log.Printf("management shutdown: %v", err)
		}
	}
	if err := supervisor.Shutdown(ctx); err != nil {
		log.Printf("gateway shutdown: %v", err)
	}
	if runErr != nil {
		log.Fatalf("management server: %v", runErr)
	}
}
