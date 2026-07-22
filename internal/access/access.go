package access

import (
	"net/http"
	"strings"
)

type Checker struct {
	keys map[string]struct{}
}

func New(keys []string) *Checker {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" {
			m[k] = struct{}{}
		}
	}
	return &Checker{keys: m}
}

func (c *Checker) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Public: health, root, and embedded monitor assets. /api/logs* still
		// require a gateway key (the browser prompts once and stores it locally).
		if r.URL.Path == "/healthz" || r.URL.Path == "/" || r.URL.Path == "/dashboard" || r.URL.Path == "/dashboard.html" || r.URL.Path == "/assets/pico-2.1.1.classless.min.css" {
			next.ServeHTTP(w, r)
			return
		}
		key := extractKey(r)
		if key == "" {
			writeUnauthorized(w, "missing api key")
			return
		}
		if _, ok := c.keys[key]; !ok {
			writeUnauthorized(w, "invalid api key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractKey(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			return strings.TrimSpace(auth[len(prefix):])
		}
		return strings.TrimSpace(auth)
	}
	if k := r.Header.Get("x-api-key"); k != "" {
		return strings.TrimSpace(k)
	}
	if k := r.Header.Get("api-key"); k != "" {
		return strings.TrimSpace(k)
	}
	return ""
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"message":"` + msg + `","type":"authentication_error","code":"invalid_api_key"}}`))
}
