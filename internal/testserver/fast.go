package testserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"
)

const fastToken = "TESTTOKEN123"

// Fast serves fast.com's page, app script and API, and Open Connect-style
// range downloads under /speedtest. Point SPEEDPROBE_FAST_URL at srv.URL
// and SPEEDPROBE_FAST_API_URL at srv.URL + "/netflix/speedtest/v2".
func Fast() *httptest.Server {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><head><script src="/app-abc123.js"></script></head></html>`))
	})
	mux.HandleFunc("GET /app-abc123.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`!function(){var e={apiEndpoint:"api.fast.com/netflix/speedtest/v2",token:"` + fastToken + `",urlCount:5}}();`))
	})
	mux.HandleFunc("GET /netflix/speedtest/v2", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != fastToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client": map[string]any{"isp": "Loopback ISP", "asn": "64512", "ip": "127.0.0.1"},
			"targets": []map[string]any{
				{"url": srv.URL + "/speedtest?c=1", "location": map[string]string{"city": "Localhost", "country": "US"}},
				{"url": srv.URL + "/speedtest?c=2", "location": map[string]string{"city": "Localhost", "country": "US"}},
			},
		})
	})
	mux.HandleFunc("GET /speedtest/range/{rng}", func(w http.ResponseWriter, r *http.Request) {
		_, endStr, ok := strings.Cut(r.PathValue("rng"), "-")
		end, err := strconv.Atoi(endStr)
		if !ok || err != nil {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		n := end + 1
		w.Header().Set("Content-Length", strconv.Itoa(n))
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for sent := 0; sent < n; {
			c := chunk[:min(len(chunk), n-sent)]
			if _, err := w.Write(c); err != nil {
				return
			}
			sent += len(c)
			if flusher != nil {
				flusher.Flush()
			}
			if n > len(chunk) {
				time.Sleep(time.Millisecond)
			}
		}
	})
	return srv
}
