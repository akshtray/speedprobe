package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// NewHTTPClient returns a client tuned for throughput measurement: HTTP/1.1
// only (so parallel streams are parallel TCP connections, not one multiplexed
// HTTP/2 connection), unlimited connections per host, proxies from the
// environment, and the given User-Agent on every request.
func NewHTTPClient(userAgent string) *http.Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConns:          0,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{Transport: uaTransport{rt: tr, ua: userAgent}}
}

type uaTransport struct {
	rt http.RoundTripper
	ua string
}

func (t uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", t.ua)
	}
	return t.rt.RoundTrip(req)
}

// CheckStatus converts a non-2xx response into an HTTPError.
func CheckStatus(resp *http.Response) error {
	if resp.StatusCode >= 400 {
		return &HTTPError{Status: resp.Status, Code: resp.StatusCode}
	}
	return nil
}

const chunk = 1 << 16

// HTTPDownload returns a worker that repeatedly GETs urlFor(i) and counts the
// body. At the deadline it drops the connection instead of draining the body.
func HTTPDownload(client *http.Client, urlFor func(i int) string) Worker {
	return func(ctx context.Context, i int, m *Meter, deadline time.Time) error {
		buf := make([]byte, chunk)
		for time.Now().Before(deadline) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlFor(i), nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			if err := CheckStatus(resp); err != nil {
				_ = resp.Body.Close()
				return err
			}
			for {
				n, err := resp.Body.Read(buf)
				m.Add(n)
				if time.Now().After(deadline) {
					_ = resp.Body.Close() // mid-body close discards the connection; that is the point
					return nil
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					_ = resp.Body.Close()
					return err
				}
			}
			_ = resp.Body.Close()
		}
		return nil
	}
}

var (
	blobOnce sync.Once
	blob     []byte
)

// randomBlob returns a shared buffer of at least n random (incompressible) bytes.
func randomBlob(n int) []byte {
	blobOnce.Do(func() {
		blob = make([]byte, n)
		if _, err := rand.Read(blob); err != nil {
			panic("engine: crypto/rand failed: " + err.Error())
		}
	})
	return blob
}

// UploadOptions tune HTTPUpload.
type UploadOptions struct {
	Start   int         // first payload size; default 256 KiB
	Max     int         // payload ceiling; default 16 MiB
	Headers http.Header // extra request headers
}

// HTTPUpload returns a worker that POSTs random payloads to urlFor(i, size).
// The payload doubles while requests complete in under a second, up to Max.
func HTTPUpload(client *http.Client, urlFor func(i, size int) string, uo UploadOptions) Worker {
	start, maxSize := uo.Start, uo.Max
	if start <= 0 {
		start = 256 * 1024
	}
	if maxSize <= 0 {
		maxSize = 16 * 1024 * 1024
	}
	return func(ctx context.Context, i int, m *Meter, deadline time.Time) error {
		size := start
		payload := randomBlob(maxSize)
		for time.Now().Before(deadline) {
			t := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlFor(i, size), bytes.NewReader(payload[:size]))
			if err != nil {
				return err
			}
			req.ContentLength = int64(size)
			for k, vs := range uo.Headers {
				req.Header[k] = vs
			}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if err := CheckStatus(resp); err != nil {
				return err
			}
			m.Add(size)
			if time.Since(t) < time.Second && size < maxSize {
				size = min(size*2, maxSize)
			}
		}
		return nil
	}
}
