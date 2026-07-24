package server_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mieluoxxx/lite-cpa/internal/config"
	"github.com/Mieluoxxx/lite-cpa/internal/server"
	"github.com/Mieluoxxx/lite-cpa/internal/translator"
)

// TestLiveResponsesUpstream is skipped unless all LITE_CPA_LIVE_* variables
// are provided. It exercises a real Responses-compatible upstream through the
// public lite-cpa HTTP endpoint without persisting the upstream credential.
func TestLiveResponsesUpstream(t *testing.T) {
	baseURL := os.Getenv("LITE_CPA_LIVE_BASE_URL")
	apiKey := os.Getenv("LITE_CPA_LIVE_API_KEY")
	model := os.Getenv("LITE_CPA_LIVE_MODEL")
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skip("live upstream environment is not configured")
	}

	translator.RegisterBuiltin()
	port := freePort(t)
	cfg := &config.Config{
		Host: "127.0.0.1", Port: port, APIKeys: []string{"live-gateway-key"},
		MaxBodyBytes: 1 << 20,
		OpenAIResponses: []config.Provider{{
			Name: "live", BaseURL: baseURL, APIKey: apiKey,
			Models: []config.ModelAlias{{Name: model, Alias: model}},
		}},
	}
	srv := server.New(cfg, nil)
	go func() { _ = srv.ListenAndServe() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)
	waitHTTP(t, endpoint+"/healthz")

	client := &http.Client{Timeout: 2 * time.Minute}
	for _, stream := range []bool{false, true} {
		body := []byte(`{"model":"` + model + `","input":"Reply with exactly OK","max_output_tokens":32,"stream":` + strconv.FormatBool(stream) + `}`)
		req, err := http.NewRequest(http.MethodPost, endpoint+"/v1/responses", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer live-gateway-key")
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("stream=%v: %v", stream, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if readErr != nil {
			t.Fatalf("stream=%v read: %v", stream, readErr)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream=%v status=%d body=%s", stream, resp.StatusCode, data)
		}
		if stream {
			if !strings.Contains(string(data), "response.completed") {
				t.Fatalf("stream response missing completion event: %s", data)
			}
		} else if !strings.Contains(string(data), `"status":"completed"`) {
			t.Fatalf("non-stream response not completed: %s", data)
		}
	}
}
