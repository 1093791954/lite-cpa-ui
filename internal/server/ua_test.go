package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Mieluoxxx/lite-cpa/internal/config"
	"github.com/Mieluoxxx/lite-cpa/internal/server"
	"github.com/Mieluoxxx/lite-cpa/internal/translator"
)

func TestProviderNameAndUserAgentHeader(t *testing.T) {
	translator.RegisterBuiltin()

	var gotUA, gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(up.Close)

	port := freePort(t)
	cfg := &config.Config{
		Host: "127.0.0.1", Port: port, APIKeys: []string{"sk-test"}, MaxBodyBytes: 1 << 20,
		AnthropicMessages: []config.Provider{{
			Name:    "claude-cli-profile",
			BaseURL: up.URL,
			APIKey:  "sk-ant",
			Models:  []config.ModelAlias{{Name: "claude-sonnet-4", Alias: "claude-sonnet-4"}},
			Headers: map[string]string{
				"User-Agent": "claude-cli/2.1.63 (external, cli)",
			},
		}},
	}
	srv := server.New(cfg, nil)
	go func() { _ = srv.ListenAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown(t.Context()) })
	waitHTTP(t, "http://127.0.0.1:"+strconv.Itoa(port)+"/healthz")

	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port)+"/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, raw)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path %q", gotPath)
	}
	if gotUA != "claude-cli/2.1.63 (external, cli)" {
		t.Fatalf("User-Agent = %q, want custom UA (not Go-http-client/1.1)", gotUA)
	}
}
