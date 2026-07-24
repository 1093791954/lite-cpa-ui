package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mieluoxxx/lite-cpa/internal/registry"
	"github.com/Mieluoxxx/lite-cpa/internal/translator"
)

func TestDiscoverModelsFallsBackToV1(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/models":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>dashboard</html>"))
		case "/v1/models":
			if got := r.Header.Get("Authorization"); got != "Bearer secret" {
				t.Errorf("Authorization = %q", got)
			}
			if got := r.Header.Get("X-Custom"); got != "yes" {
				t.Errorf("X-Custom = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"z-model"},{"id":"a-model"},{"id":"a-model"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	result, err := DiscoverModels(t.Context(), registry.UpstreamKey{
		Provider: "openai-response", BaseURL: upstream.URL, APIKey: "secret",
		Headers: map[string]string{"X-Custom": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
	if result.ResolvedBaseURL != upstream.URL+"/v1" {
		t.Fatalf("resolved base = %q", result.ResolvedBaseURL)
	}
	if strings.Join(result.Models, ",") != "a-model,z-model" {
		t.Fatalf("models = %v", result.Models)
	}
}

func TestDiscoverModelsUsesAnthropicHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("x-api-key"); got != "anthropic-secret" {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("anthropic-version = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-test"}]}`))
	}))
	defer upstream.Close()

	result, err := DiscoverModels(t.Context(), registry.UpstreamKey{
		Provider: "claude", BaseURL: upstream.URL + "/v1", APIKey: "anthropic-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 1 || result.Models[0] != "claude-test" {
		t.Fatalf("models = %v", result.Models)
	}
}

func TestResponsesRejectsHTMLSuccess(t *testing.T) {
	translator.RegisterBuiltin()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>not the API</html>"))
	}))
	defer upstream.Close()

	key := registry.UpstreamKey{Provider: "openai-response", BaseURL: upstream.URL, APIKey: "secret"}
	for _, stream := range []bool{false, true} {
		_, err := Execute(context.Background(), key, "gpt-test", translator.FormatOpenAIResponse,
			[]byte(`{"model":"gpt-test","input":"hello"}`), stream)
		statusErr, ok := err.(StatusError)
		if !ok || statusErr.Code != http.StatusBadGateway || !strings.Contains(statusErr.Body, "needs /v1") {
			t.Fatalf("stream=%v err=%T %v", stream, err, err)
		}
	}
}
