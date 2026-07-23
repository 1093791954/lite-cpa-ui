package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Mieluoxxx/lite-cpa/internal/access"
	"github.com/Mieluoxxx/lite-cpa/internal/affinity"
	"github.com/Mieluoxxx/lite-cpa/internal/config"
	"github.com/Mieluoxxx/lite-cpa/internal/executor"
	"github.com/Mieluoxxx/lite-cpa/internal/pool"
	"github.com/Mieluoxxx/lite-cpa/internal/registry"
	"github.com/Mieluoxxx/lite-cpa/internal/reqlog"
	"github.com/Mieluoxxx/lite-cpa/internal/thinking"
	"github.com/Mieluoxxx/lite-cpa/internal/translator"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type Server struct {
	cfg      *config.Config
	reg      *registry.Registry
	selector *pool.Selector
	affinity *affinity.Manager
	auth     *access.Checker
	logger   *reqlog.Logger
	http     *http.Server
}

func New(cfg *config.Config, logger *reqlog.Logger) *Server {
	if logger == nil {
		logger = &reqlog.Logger{}
	}
	reg := pool.BuildRegistry(cfg)
	s := &Server{
		cfg:      cfg,
		reg:      reg,
		selector: pool.NewSelector(reg, cfg.RequestRetry),
		affinity: affinity.New(cfg.ChannelAffinity),
		auth:     access.New(cfg.APIKeys),
		logger:   logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /", s.handleRoot)
	mux.HandleFunc("GET /dashboard", s.handleDashboard)
	mux.HandleFunc("GET /dashboard.html", s.handleDashboard)
	mux.HandleFunc("GET /assets/pico-2.1.1.classless.min.css", s.handlePicoClasslessCSS)
	mux.HandleFunc("GET /api/logs", s.handleLogsList)
	mux.HandleFunc("DELETE /api/logs", s.handleLogsClear)
	mux.HandleFunc("GET /api/logs/stats", s.handleLogsStats)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /v1/responses", s.handleResponses)
	mux.HandleFunc("POST /v1/messages", s.handleMessages)
	handler := s.auth.Middleware(limitBody(cfg.MaxBodyBytes, withRecover(mux)))
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	s.http = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

func (s *Server) ListenAndServe() error {
	log.Printf("lite-cpa listening on %s", s.http.Addr)
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.affinity != nil {
		s.affinity.Close()
	}
	return s.http.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"name":"lite-cpa","endpoints":["GET /dashboard","GET /dashboard.html","GET /api/logs","DELETE /api/logs","GET /api/logs/stats","GET /v1/models","POST /v1/chat/completions","POST /v1/responses","POST /v1/messages"]}`))
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	models := s.reg.List()
	data := make([]map[string]any, 0, len(models))
	for _, m := range models {
		data = append(data, map[string]any{
			"id": m.ID, "object": "model", "created": m.Created, "owned_by": m.OwnedBy,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.handleProxy(w, r, translator.FormatOpenAI)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.handleProxy(w, r, translator.FormatOpenAIResponse)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	s.handleProxy(w, r, translator.FormatClaude)
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request, source translator.Format) {
	start := time.Now()
	reqID := uuid.NewString()
	protocol := protocolOf(source)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("read body: %v", err))
		s.logReq(reqID, r, protocol, "", "", "", http.StatusBadRequest, start, err.Error(), body, nil)
		return
	}
	model := gjson.GetBytes(body, "model").String()
	if model == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		s.logReq(reqID, r, protocol, "", "", "", http.StatusBadRequest, start, "model is required", body, nil)
		return
	}
	baseModel := thinking.ParseSuffix(model).ModelName
	stream := gjson.GetBytes(body, "stream").Bool()

	resolveName := model
	if _, _, ok := s.reg.Resolve(model); !ok {
		resolveName = baseModel
	}

	aff := s.affinity.Lookup(resolveName, r.URL.Path, r.Header, body)
	if aff.Found {
		if s.cfg.Debug {
			log.Printf("affinity hit rule=%s key=%s", aff.RuleName, aff.KeyID)
		}
	}

	tried := make(map[string]struct{})
	skipSuppliers := make(map[string]struct{})
	preferSupplier := ""
	maxAttempts := s.selector.MaxAttempts(resolveName)
	// skip-retry only applies when a sticky key is actually bound (new-api MarkChannelAffinityUsed).
	if aff.Found && aff.SkipRetry {
		maxAttempts = 1
	}
	var lastErr error
	lastErrLogged := false
	var lastKey registry.UpstreamKey
	for attempt := range maxAttempts {
		var key registry.UpstreamKey
		var upstreamModel string
		var pickErr error

		// First attempt: honor sticky preferred key if still available.
		if attempt == 0 && aff.Found {
			if _, keys, ok := s.reg.Resolve(resolveName); ok {
				if preferred, ok := affinity.ResolvePreferred(keys, aff.KeyID, tried); ok {
					// sticky key may still belong to a skipped supplier only on later attempts
					key = preferred
					upstreamModel = resolveName
					if preferred.Headers != nil {
						if m := preferred.Headers["x-lite-upstream-model"]; m != "" {
							upstreamModel = m
						}
					}
					preferSupplier = preferred.Name
				}
			}
		}
		if key.ID == "" {
			key, upstreamModel, pickErr = s.selector.Pick(resolveName, tried, preferSupplier, skipSuppliers)
			if pickErr != nil {
				lastErr = pickErr
				lastErrLogged = false
				break
			}
			if preferSupplier == "" {
				preferSupplier = key.Name
			}
		}
		tried[key.ID] = struct{}{}
		lastKey = key

		if thinking.ParseSuffix(model).HasSuffix {
			suffix := thinking.ParseSuffix(model).RawSuffix
			upstreamModel = thinking.ParseSuffix(upstreamModel).ModelName + "(" + suffix + ")"
		}

		attemptStart := time.Now()
		result, err := executor.Execute(r.Context(), key, upstreamModel, source, body, stream)
		if err != nil {
			lastErr = err
			lastErrLogged = false
			// Sticky key failed: drop pin so subsequent requests rebalance.
			if aff.Matched && aff.CacheKey != "" && key.ID == aff.KeyID {
				s.affinity.Clear(aff.CacheKey)
				if s.cfg.Debug {
					log.Printf("affinity cleared rule=%s key=%s", aff.RuleName, key.ID)
				}
			}
			if se, ok := err.(executor.StatusError); ok {
				if se.Code == 401 || se.Code == 403 || se.Code == 429 || se.Code >= 500 {
					s.logReq(reqID, r, protocol, model, key.Provider, key.Name, se.Code, attemptStart, se.Error(), body, nil)
					lastErrLogged = true
					if s.cfg.Debug {
						log.Printf("upstream %s/%s failed status=%d, rotating (mode=%s)", key.Name, key.ID, se.Code, key.FailoverMode)
					}
					// skip-retry-on-failure: do not rotate after a sticky preferred key failed
					if aff.Found && aff.SkipRetry && key.ID == aff.KeyID {
						break
					}
					// per-provider mode: one bad key ⇒ skip remaining keys of this supplier
					if key.FailoverMode == "provider" {
						skipSuppliers[key.Name] = struct{}{}
						if preferSupplier == key.Name {
							preferSupplier = ""
						}
					}
					continue
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(se.Code)
				_, _ = w.Write([]byte(se.Body))
				s.logReq(reqID, r, protocol, model, key.Provider, key.Name, se.Code, start, se.Error(), body, []byte(se.Body))
				return
			}
			s.logReq(reqID, r, protocol, model, key.Provider, key.Name, http.StatusBadGateway, attemptStart, err.Error(), body, nil)
			lastErrLogged = true
			if aff.Found && aff.SkipRetry && key.ID == aff.KeyID {
				break
			}
			if key.FailoverMode == "provider" {
				skipSuppliers[key.Name] = struct{}{}
				if preferSupplier == key.Name {
					preferSupplier = ""
				}
			}
			continue
		}

		// Success: pin (or refresh) sticky binding.
		if aff.Matched {
			// switch-on-success (default true): always pin the key that succeeded.
			// when false: only pin if we have no prior pin or the preferred key succeeded.
			if s.cfg.ChannelAffinity.SwitchOnSuccessOrDefault() || !aff.Found || aff.KeyID == key.ID {
				s.affinity.Record(aff.CacheKey, key.ID, aff.TTL)
				if s.cfg.Debug {
					log.Printf("affinity recorded rule=%s key=%s", aff.RuleName, key.ID)
				}
			}
		}

		switch v := result.(type) {
		case *executor.Result:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(v.Body)
			usage := usageFromResponse(v.Body)
			s.logReq(reqID, r, protocol, model, key.Provider, key.Name, http.StatusOK, start, "", body, v.Body, usage)
			return
		case *executor.StreamResult:
			flusher, ok := w.(http.Flusher)
			if !ok {
				writeAPIError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
				s.logReq(reqID, r, protocol, model, key.Provider, key.Name, http.StatusInternalServerError, start, "streaming not supported", body, nil)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)
			flusher.Flush()
			var streamErr string
			var usage tokenUsage
			for chunk := range v.Chunks {
				if chunk.Err != nil {
					if s.cfg.Debug {
						log.Printf("stream error: %v", chunk.Err)
					}
					streamErr = chunk.Err.Error()
					break
				}
				if chunk.LogError != "" && streamErr == "" {
					streamErr = chunk.LogError
				}
				if len(chunk.Payload) == 0 {
					continue
				}
				usage.mergePayload(chunk.Payload)
				if _, err := w.Write(chunk.Payload); err != nil {
					streamErr = err.Error()
					break
				}
				flusher.Flush()
			}
			// Streaming responses: do not store response body by default (memory).
			s.logReq(reqID, r, protocol, model, key.Provider, key.Name, http.StatusOK, start, streamErr, body, nil, usage)
			return
		default:
			lastErr = fmt.Errorf("unexpected executor result type %T", result)
			lastErrLogged = false
		}
	}

	if lastErr != nil {
		if se, ok := lastErr.(executor.StatusError); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(se.Code)
			_, _ = w.Write([]byte(se.Body))
			if !lastErrLogged {
				s.logReq(reqID, r, protocol, model, lastKey.Provider, lastKey.Name, se.Code, start, se.Error(), body, []byte(se.Body))
			}
			return
		}
		msg := lastErr.Error()
		code := http.StatusBadGateway
		if strings.Contains(msg, "model not found") {
			code = http.StatusNotFound
		}
		writeAPIError(w, code, "server_error", msg)
		if !lastErrLogged {
			s.logReq(reqID, r, protocol, model, lastKey.Provider, lastKey.Name, code, start, msg, body, nil)
		}
		return
	}
	writeAPIError(w, http.StatusBadGateway, "server_error", "all upstream credentials failed")
	s.logReq(reqID, r, protocol, model, lastKey.Provider, lastKey.Name, http.StatusBadGateway, start, "all upstream credentials failed", body, nil)
}

func (s *Server) logReq(id string, r *http.Request, protocol, model, provider, upstream string, status int, start time.Time, errMsg string, reqBody, respBody []byte, usage ...tokenUsage) {
	if s.logger == nil || !s.logger.Enabled() {
		return
	}
	rec := reqlog.Record{
		RequestID:  id,
		Timestamp:  start,
		Method:     r.Method,
		Path:       r.URL.Path,
		StatusCode: status,
		Model:      model,
		Protocol:   protocol,
		Provider:   provider,
		Upstream:   upstream,
		UserAgent:  r.UserAgent(),
		DurationMS: time.Since(start).Milliseconds(),
		Error:      errMsg,
	}
	if len(usage) > 0 {
		rec.InputTokens = usage[0].inputTokens
		rec.OutputTokens = usage[0].outputTokens
		rec.CachedTokens = usage[0].cachedTokens
	}
	if s.cfg.RequestLog.StoreBody {
		// Cap stored bodies to keep memory/disk bounded.
		rec.ReqBody = truncate(string(reqBody), 64<<10)
		rec.RespBody = truncate(string(respBody), 64<<10)
	}
	s.logger.Record(rec)
}

func protocolOf(source translator.Format) string {
	switch source {
	case translator.FormatOpenAI:
		return "chat"
	case translator.FormatOpenAIResponse:
		return "responses"
	case translator.FormatClaude:
		return "claude"
	default:
		return source.String()
	}
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": msg, "type": typ},
	})
}

func limitBody(max int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && max > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		next.ServeHTTP(w, r)
	})
}

func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				writeAPIError(w, http.StatusInternalServerError, "server_error", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
