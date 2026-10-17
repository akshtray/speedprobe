package testserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	ndt7Protocol = "net.measurementlab.ndt.v7"
	ndt7TestLen  = 1500 * time.Millisecond // real servers run ~10 s; tests need not
)

// MLab serves a Locate API stand-in at /locate and an ndt7 server at
// /ndt/v7/download and /ndt/v7/upload. Each direction lasts about 1.5 s. The
// server reports MinRTT 12 ms, RTT 25 ms and RTTVar 3 ms in every TCPInfo.
func MLab() *httptest.Server { return mlab(false) }

// MLabWithDeadFirstTarget is MLab whose locate response lists an unreachable
// machine first, so clients must fall back to the second target.
func MLabWithDeadFirstTarget() *httptest.Server { return mlab(true) }

func mlab(deadFirst bool) *httptest.Server {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	host := strings.TrimPrefix(srv.URL, "http://")

	target := func(machine, host string) map[string]any {
		return map[string]any{
			"machine":  machine,
			"hostname": host,
			"location": map[string]string{"city": "Localhost", "country": "US"},
			"urls": map[string]string{
				"ws:///ndt/v7/download": "ws://" + host + "/ndt/v7/download",
				"ws:///ndt/v7/upload":   "ws://" + host + "/ndt/v7/upload",
			},
		}
	}
	mux.HandleFunc("GET /locate", func(w http.ResponseWriter, r *http.Request) {
		results := []map[string]any{}
		if deadFirst {
			results = append(results, target("mlab-dead", "127.0.0.1:1"))
		}
		results = append(results, target("mlab-mock", host))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	})

	upgrader := websocket.Upgrader{Subprotocols: []string{ndt7Protocol}, CheckOrigin: func(*http.Request) bool { return true }}

	mux.HandleFunc("GET /ndt/v7/download", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		t0 := time.Now()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"ConnectionInfo":{"Client":"127.0.0.1:1","Server":"127.0.0.1:2","UUID":"mock-uuid"}}`))
		last := t0
		for time.Since(t0) < ndt7TestLen {
			if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
				return
			}
			time.Sleep(2 * time.Millisecond)
			if time.Since(last) > 250*time.Millisecond {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(tcpInfo(t0, 0)))
				last = time.Now()
			}
		}
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	})

	mux.HandleFunc("GET /ndt/v7/upload", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		conn.SetReadLimit(1 << 24)
		t0 := time.Now()
		end := t0.Add(ndt7TestLen)
		var got int64
		last := t0
		for time.Now().Before(end) {
			_ = conn.SetReadDeadline(end)
			mt, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if mt == websocket.BinaryMessage {
				got += int64(len(data))
			}
			if time.Since(last) > 250*time.Millisecond {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(tcpInfo(t0, got))); err != nil {
					return
				}
				last = time.Now()
			}
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(tcpInfo(t0, got)))
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	})
	return srv
}

// tcpInfo is a server-origin ndt7 measurement message with fixed RTTs and,
// for uploads, the bytes received so far.
func tcpInfo(t0 time.Time, received int64) string {
	elapsed := time.Since(t0).Microseconds()
	return fmt.Sprintf(`{"AppInfo":{"NumBytes":%d,"ElapsedTime":%d},"TCPInfo":{"MinRTT":12000,"RTT":25000,"RTTVar":3000,"BytesReceived":%d,"ElapsedTime":%d}}`,
		received, elapsed, received, elapsed)
}
