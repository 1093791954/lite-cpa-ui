package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mieluoxxx/lite-cpa/internal/config"
	"github.com/Mieluoxxx/lite-cpa/internal/executor"
	"github.com/Mieluoxxx/lite-cpa/internal/registry"
)

const maxAdminBody = 8 << 20

// AdminServer serves the unauthenticated, local-only management UI and API.
// It deliberately does not share a listener with the public gateway.
type AdminServer struct {
	configPath string
	addr       string
	supervisor *Supervisor
	http       *http.Server
	applyMu    sync.Mutex
	started    time.Time
	stateMu    sync.RWMutex
	applying   bool
	lastError  string
}

type configRequest struct {
	YAML             string `json:"yaml"`
	ExpectedRevision string `json:"expected_revision"`
}

type providerModelsRequest struct {
	ProviderType string            `json:"provider_type"`
	BaseURL      string            `json:"base_url"`
	APIKey       string            `json:"api_key"`
	ProxyURL     string            `json:"proxy_url"`
	Headers      map[string]string `json:"headers"`
}

func NewAdminServer(configPath, addr string, supervisor *Supervisor) *AdminServer {
	a := &AdminServer{configPath: configPath, addr: addr, supervisor: supervisor, started: time.Now()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /api/config", a.handleConfigGet)
	mux.HandleFunc("POST /api/config/validate", a.handleConfigValidate)
	mux.HandleFunc("PUT /api/config", a.handleConfigSave)
	mux.HandleFunc("POST /api/config/apply", a.handleConfigApply)
	mux.HandleFunc("POST /api/config/reload", a.handleConfigReload)
	mux.HandleFunc("POST /api/config/rollback", a.handleConfigRollback)
	mux.HandleFunc("POST /api/providers/models", a.handleProviderModels)
	mux.HandleFunc("GET /api/logs", a.handleAdminLogsList)
	mux.HandleFunc("DELETE /api/logs", a.handleAdminLogsClear)
	mux.HandleFunc("GET /api/logs/stats", a.handleAdminLogsStats)
	mux.HandleFunc("GET /", a.handleAdminAsset)
	a.http = &http.Server{
		Addr:              addr,
		Handler:           withRecover(a.localOnly(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return a
}

func (a *AdminServer) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req providerModelsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	provider := ""
	switch strings.ToLower(strings.TrimSpace(req.ProviderType)) {
	case "openai-responses", "openai-response", "responses":
		provider = "openai-response"
	case "openai-completions", "openai", "chat":
		provider = "openai"
	case "anthropic-messages", "anthropic", "claude":
		provider = "claude"
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported provider_type")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := executor.DiscoverModels(ctx, registry.UpstreamKey{
		Provider: provider,
		BaseURL:  strings.TrimSpace(req.BaseURL),
		APIKey:   strings.TrimSpace(req.APIKey),
		ProxyURL: strings.TrimSpace(req.ProxyURL),
		Headers:  req.Headers,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "model_discovery_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"models": result.Models, "resolved_base_url": result.ResolvedBaseURL,
	})
}

func (a *AdminServer) ListenAndServe() error {
	return a.http.ListenAndServe()
}

func (a *AdminServer) Shutdown(ctx context.Context) error { return a.http.Shutdown(ctx) }

func (a *AdminServer) localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		host = strings.Trim(host, "[]")
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			writeAPIError(w, http.StatusForbidden, "forbidden", "management host must be localhost")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
				writeAPIError(w, http.StatusUnsupportedMediaType, "invalid_request_error", "Content-Type must be application/json")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeAPIError(w, http.StatusForbidden, "forbidden", "cross-origin management request rejected")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *AdminServer) handleAdminAsset(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	root, err := fs.Sub(dashboardFiles, "dashboard")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(root, path); err != nil {
		path = "index.html"
	}
	if path == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
		index, readErr := fs.ReadFile(root, path)
		if readErr != nil {
			http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
		return
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	r.URL.Path = "/" + path
	http.FileServerFS(root).ServeHTTP(w, r)
}

func (a *AdminServer) handleStatus(w http.ResponseWriter, _ *http.Request) {
	cfg, logger, gatewayAddr, appliedRevision, gatewayStarted := a.supervisor.Snapshot()
	_, diskRevision, diskErr := a.readConfig()
	a.stateMu.RLock()
	applying, lastError := a.applying, a.lastError
	a.stateMu.RUnlock()
	providerCount := len(cfg.AnthropicMessages) + len(cfg.OpenAIResponses) + len(cfg.OpenAICompletions)
	modelCount := 0
	for _, providers := range [][]config.Provider{cfg.AnthropicMessages, cfg.OpenAIResponses, cfg.OpenAICompletions} {
		for _, provider := range providers {
			modelCount += len(provider.Models)
		}
	}
	status := map[string]any{
		"status": "ok", "gateway_addr": gatewayAddr, "admin_addr": a.addr,
		"config_path": a.configPath, "disk_revision": diskRevision,
		"applied_revision": appliedRevision, "in_sync": diskErr == nil && diskRevision == appliedRevision,
		"applying": applying, "last_error": lastError, "request_log_enabled": logger.Enabled(),
		"provider_count": providerCount, "model_count": modelCount,
		"gateway_started_at": gatewayStarted, "admin_started_at": a.started,
	}
	if diskErr != nil {
		status["disk_error"] = diskErr.Error()
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *AdminServer) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	data, revision, err := a.readConfig()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "config_error", err.Error())
		return
	}
	if _, err := config.Parse(data); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "config_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"yaml": string(data), "revision": revision})
}

func (a *AdminServer) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeConfigRequest(w, r)
	if !ok {
		return
	}
	if _, err := config.Parse([]byte(req.YAML)); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
}

func (a *AdminServer) handleConfigSave(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeConfigRequest(w, r)
	if !ok {
		return
	}
	if _, err := config.Parse([]byte(req.YAML)); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "config_error", err.Error())
		return
	}
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	revision, err := a.save([]byte(req.YAML), req.ExpectedRevision)
	if err != nil {
		a.writeSaveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "revision": revision, "applied": false})
}

func (a *AdminServer) handleConfigApply(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeConfigRequest(w, r)
	if !ok {
		return
	}
	cfg, err := config.Parse([]byte(req.YAML))
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "config_error", err.Error())
		return
	}
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	a.setApplying(true)
	defer a.setApplying(false)
	revision, err := a.save([]byte(req.YAML), req.ExpectedRevision)
	if err != nil {
		a.setLastError(err)
		a.writeSaveError(w, err)
		return
	}
	if err := a.supervisor.Apply(cfg, revision); err != nil {
		if restoreErr := a.restoreBackup(); restoreErr != nil {
			err = fmt.Errorf("%w; restore config: %v", err, restoreErr)
		}
		a.setLastError(err)
		writeAPIError(w, http.StatusInternalServerError, "apply_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "applied": true, "revision": revision})
}

func (a *AdminServer) handleConfigReload(w http.ResponseWriter, _ *http.Request) {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	data, revision, err := a.readConfig()
	if err == nil {
		var cfg *config.Config
		cfg, err = config.Parse(data)
		if err == nil {
			err = a.supervisor.Apply(cfg, revision)
		}
	}
	if err != nil {
		a.setLastError(err)
		writeAPIError(w, http.StatusInternalServerError, "reload_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": true, "revision": revision})
}

func (a *AdminServer) handleConfigRollback(w http.ResponseWriter, _ *http.Request) {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	backup, err := os.ReadFile(a.configPath + ".bak")
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "rollback_error", "no previous configuration backup")
		return
	}
	cfg, err := config.Parse(backup)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "rollback_error", err.Error())
		return
	}
	_, currentRevision, err := a.readConfig()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "rollback_error", err.Error())
		return
	}
	revision, err := a.save(backup, currentRevision)
	if err == nil {
		err = a.supervisor.Apply(cfg, revision)
	}
	if err != nil {
		a.setLastError(err)
		writeAPIError(w, http.StatusInternalServerError, "rollback_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rolled_back": true, "applied": true, "revision": revision})
}

func (a *AdminServer) handleAdminLogsStats(w http.ResponseWriter, r *http.Request) {
	_, logger, _, _, _ := a.supervisor.Snapshot()
	st, err := logger.Stats(r.Context(), logListFilter(r.URL.Query()))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *AdminServer) handleAdminLogsList(w http.ResponseWriter, r *http.Request) {
	_, logger, _, _, _ := a.supervisor.Snapshot()
	q := r.URL.Query()
	f := logListFilter(q)
	if n, err := strconv.Atoi(q.Get("limit")); err == nil {
		f.Limit = n
	}
	if n, err := strconv.Atoi(q.Get("offset")); err == nil {
		f.Offset = n
	}
	res, err := logger.List(r.Context(), f)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *AdminServer) handleAdminLogsClear(w http.ResponseWriter, r *http.Request) {
	_, logger, _, _, _ := a.supervisor.Snapshot()
	deleted, err := logger.Clear(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}

func decodeConfigRequest(w http.ResponseWriter, r *http.Request) (configRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	var req configRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return req, false
	}
	if strings.TrimSpace(req.YAML) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "yaml is required")
		return req, false
	}
	return req, true
}

// ConfigRevision returns the stable revision used for optimistic config writes.
func ConfigRevision(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (a *AdminServer) readConfig() ([]byte, string, error) {
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return nil, "", err
	}
	return data, ConfigRevision(data), nil
}

var errRevisionConflict = errors.New("configuration changed on disk")

func (a *AdminServer) save(data []byte, expected string) (string, error) {
	_, currentRevision, err := a.readConfig()
	if err != nil {
		return "", err
	}
	if expected == "" || expected != currentRevision {
		return "", errRevisionConflict
	}
	info, err := os.Stat(a.configPath)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(a.configPath)
	temp, err := os.CreateTemp(dir, ".lite-cpa-config-*")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		temp.Close()
		return "", err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	backup := a.configPath + ".bak"
	_ = os.Remove(backup)
	if err := os.Rename(a.configPath, backup); err != nil {
		return "", err
	}
	if err := os.Rename(tempName, a.configPath); err != nil {
		_ = os.Rename(backup, a.configPath)
		return "", err
	}
	return ConfigRevision(data), nil
}

func (a *AdminServer) restoreBackup() error {
	backup := a.configPath + ".bak"
	failed := a.configPath + ".failed"
	_ = os.Remove(failed)
	if err := os.Rename(a.configPath, failed); err != nil {
		return err
	}
	if err := os.Rename(backup, a.configPath); err != nil {
		_ = os.Rename(failed, a.configPath)
		return err
	}
	_ = os.Remove(failed)
	return nil
}

func (a *AdminServer) writeSaveError(w http.ResponseWriter, err error) {
	if errors.Is(err, errRevisionConflict) {
		writeAPIError(w, http.StatusConflict, "revision_conflict", err.Error())
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "save_error", err.Error())
}

func (a *AdminServer) setApplying(value bool) {
	a.stateMu.Lock()
	a.applying = value
	if value {
		a.lastError = ""
	}
	a.stateMu.Unlock()
}

func (a *AdminServer) setLastError(err error) {
	a.stateMu.Lock()
	a.lastError = err.Error()
	a.stateMu.Unlock()
}
