package server_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Mieluoxxx/lite-cpa/internal/config"
	"github.com/Mieluoxxx/lite-cpa/internal/server"
)

func TestDashboardAliases(t *testing.T) {
	port := freePort(t)
	srv := server.New(&config.Config{
		Host:         "127.0.0.1",
		Port:         port,
		APIKeys:      []string{"sk-test"},
		MaxBodyBytes: 1 << 20,
	}, nil)
	go func() { _ = srv.ListenAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown(t.Context()) })

	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	waitHTTP(t, baseURL+"/healthz")

	var dashboard string
	for _, path := range []string{"/dashboard", "/dashboard.html"} {
		resp, err := http.Get(baseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status %d body %s", path, resp.StatusCode, body)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("GET %s Content-Type %q", path, resp.Header.Get("Content-Type"))
		}
		if !strings.Contains(string(body), "lite-cpa · Logs") {
			t.Fatalf("GET %s did not return dashboard HTML", path)
		}
		if dashboard != "" && dashboard != string(body) {
			t.Fatal("dashboard aliases returned different content")
		}
		dashboard = string(body)
	}

	cssResp, err := http.Get(baseURL + "/assets/pico-2.1.1.classless.min.css")
	if err != nil {
		t.Fatalf("GET Pico CSS: %v", err)
	}
	cssBody, readErr := io.ReadAll(cssResp.Body)
	cssResp.Body.Close()
	if readErr != nil {
		t.Fatalf("read Pico CSS: %v", readErr)
	}
	if cssResp.StatusCode != http.StatusOK {
		t.Fatalf("GET Pico CSS status %d body %s", cssResp.StatusCode, cssBody)
	}
	if !strings.HasPrefix(cssResp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("GET Pico CSS Content-Type %q", cssResp.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(cssBody), "Pico CSS") {
		t.Fatal("GET Pico CSS did not return the vendored stylesheet")
	}
}
