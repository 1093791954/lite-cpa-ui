package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Mieluoxxx/lite-cpa/internal/config"
	"github.com/Mieluoxxx/lite-cpa/internal/server"
)

func TestAdminConfigApplyAndRollback(t *testing.T) {
	gatewayPort := freePort(t)
	adminPort := freePort(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	initial := testConfigYAML(gatewayPort, "sk-old")
	if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse([]byte(initial))
	if err != nil {
		t.Fatal(err)
	}
	supervisor, err := server.NewSupervisor(cfg, server.ConfigRevision([]byte(initial)))
	if err != nil {
		t.Fatal(err)
	}
	admin := server.NewAdminServer(configPath, "127.0.0.1:"+strconv.Itoa(adminPort), supervisor)
	go func() { _ = admin.ListenAndServe() }()
	t.Cleanup(func() {
		_ = admin.Shutdown(t.Context())
		_ = supervisor.Shutdown(t.Context())
	})

	adminURL := "http://127.0.0.1:" + strconv.Itoa(adminPort)
	gatewayURL := "http://127.0.0.1:" + strconv.Itoa(gatewayPort)
	waitHTTP(t, adminURL+"/api/status")
	waitHTTP(t, gatewayURL+"/healthz")
	uiResp, err := http.Get(adminURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	uiBody, _ := io.ReadAll(uiResp.Body)
	uiResp.Body.Close()
	if uiResp.StatusCode != http.StatusOK || !strings.Contains(string(uiBody), "lite-cpa") || !strings.Contains(string(uiBody), "/assets/") {
		t.Fatalf("management UI status=%d body=%s", uiResp.StatusCode, uiBody)
	}

	resp, err := http.Get(adminURL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	var current struct {
		YAML     string `json:"yaml"`
		Revision string `json:"revision"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&current); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(current.YAML, "sk-old") || current.Revision == "" {
		t.Fatalf("management config did not return plaintext config/revision: %+v", current)
	}

	next := testConfigYAML(gatewayPort, "sk-new")
	payload, _ := json.Marshal(map[string]string{"yaml": next, "expected_revision": current.Revision})
	req, _ := http.NewRequest(http.MethodPost, adminURL+"/api/config/apply", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", resp.StatusCode, body)
	}
	if _, err := os.Stat(configPath + ".bak"); err != nil {
		t.Fatalf("backup not written: %v", err)
	}
	assertModelsAuth(t, gatewayURL, "sk-new", http.StatusOK)
	assertModelsAuth(t, gatewayURL, "sk-old", http.StatusUnauthorized)

	req, _ = http.NewRequest(http.MethodPost, adminURL+"/api/config/rollback", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", resp.StatusCode, body)
	}
	assertModelsAuth(t, gatewayURL, "sk-old", http.StatusOK)
}

func TestAdminRejectsCrossOriginWrite(t *testing.T) {
	gatewayPort := freePort(t)
	adminPort := freePort(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(testConfigYAML(gatewayPort, "sk-test"))
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Parse(data)
	supervisor, err := server.NewSupervisor(cfg, server.ConfigRevision(data))
	if err != nil {
		t.Fatal(err)
	}
	admin := server.NewAdminServer(configPath, "127.0.0.1:"+strconv.Itoa(adminPort), supervisor)
	go func() { _ = admin.ListenAndServe() }()
	t.Cleanup(func() {
		_ = admin.Shutdown(t.Context())
		_ = supervisor.Shutdown(t.Context())
	})
	baseURL := "http://127.0.0.1:" + strconv.Itoa(adminPort)
	waitHTTP(t, baseURL+"/api/status")
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/config/reload", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d want 403", resp.StatusCode)
	}
}

func TestAdminDiscoversProviderModelsAndResolvesV1(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>dashboard</html>"))
		case "/v1/models":
			if got := r.Header.Get("Authorization"); got != "Bearer upstream-secret" {
				t.Errorf("Authorization = %q", got)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	gatewayPort := freePort(t)
	adminPort := freePort(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(testConfigYAML(gatewayPort, "sk-test"))
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Parse(data)
	supervisor, err := server.NewSupervisor(cfg, server.ConfigRevision(data))
	if err != nil {
		t.Fatal(err)
	}
	admin := server.NewAdminServer(configPath, "127.0.0.1:"+strconv.Itoa(adminPort), supervisor)
	go func() { _ = admin.ListenAndServe() }()
	t.Cleanup(func() {
		_ = admin.Shutdown(t.Context())
		_ = supervisor.Shutdown(t.Context())
	})
	baseURL := "http://127.0.0.1:" + strconv.Itoa(adminPort)
	waitHTTP(t, baseURL+"/api/status")

	payload, _ := json.Marshal(map[string]any{
		"provider_type": "openai-responses", "base_url": upstream.URL, "api_key": "upstream-secret",
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/providers/models", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Models          []string `json:"models"`
		ResolvedBaseURL string   `json:"resolved_base_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(result.Models) != 1 || result.Models[0] != "gpt-test" || result.ResolvedBaseURL != upstream.URL+"/v1" {
		t.Fatalf("status=%d result=%+v", resp.StatusCode, result)
	}
}

func assertModelsAuth(t *testing.T, baseURL, key string, want int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("models with key %q status=%d want=%d", key, resp.StatusCode, want)
	}
}

func testConfigYAML(port int, key string) string {
	return "host: 127.0.0.1\nport: " + strconv.Itoa(port) + "\napi-keys:\n  - " + key + "\nopenai-completions:\n  - name: mock\n    base-url: http://127.0.0.1:1/v1\n    api-key: upstream\n    models:\n      - name: test-model\n        alias: test-model\n"
}
