// Package testserver provides local stand-ins for provider endpoints so the
// engine and providers can be tested without the internet. It is the Go
// counterpart of the prototype's mock_server.py.
package testserver

import (
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"
)

var chunk = func() []byte {
	b := make([]byte, 1<<16)
	_, _ = rand.Read(b)
	return b
}()

// Cloudflare serves speed.cloudflare.com's __down, __up, meta and
// cdn-cgi/trace endpoints. Downloads are throttled to roughly 64 MB/s so
// rates are stable in tests.
func Cloudflare() *httptest.Server { return cloudflare(false) }

// CloudflareWithoutMeta is Cloudflare as it behaves for non-browser clients
// today: /meta answers 403 and only /cdn-cgi/trace describes the client.
func CloudflareWithoutMeta() *httptest.Server { return cloudflare(true) }

func cloudflare(metaForbidden bool) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__down", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		w.Header().Set("Server-Timing", "cfRequestDuration;dur=0.5")
		w.Header().Set("Cf-Ray", "abc123-SEA")
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
			time.Sleep(time.Millisecond)
		}
	})
	mux.HandleFunc("POST /__up", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /meta", func(w http.ResponseWriter, r *http.Request) {
		if metaForbidden {
			http.Error(w, "{}", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"colo": "SEA", "clientIp": "127.0.0.1", "asOrganization": "Loopback", "country": "US",
		})
	})
	mux.HandleFunc("GET /cdn-cgi/trace", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "fl=1\nh=localhost\nip=127.0.0.2\nts=0\nuag=test\ncolo=SEA\nhttp=http/1.1\nloc=US\n")
	})
	return httptest.NewServer(mux)
}
