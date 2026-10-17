#!/usr/bin/env bash
# Runs the Python prototype and the Go port against the prototype's mock
# server with identical flags and compares the JSON they emit.
# Needs: uv, jq, go.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${PORT:-8765}
ARGS=(run -p cf -d "${DURATION:-3}" -c 2 --pings 5 --json -q)

uv run --with aiohttp python mock_server.py "$PORT" &
MOCK=$!
trap 'kill $MOCK 2>/dev/null || true' EXIT
for _ in $(seq 1 60); do
  curl -s -o /dev/null "http://127.0.0.1:$PORT/meta" && break
  sleep 0.5
done

export SPEEDPROBE_CLOUDFLARE_URL="http://127.0.0.1:$PORT"
echo ">> python"; uv run speedprobe.py "${ARGS[@]}" | tee /tmp/speedprobe-py.json
echo ">> go";     go run ./cmd/speedprobe "${ARGS[@]}" | tee /tmp/speedprobe-go.json

py_keys=$(jq -c '.[0] | keys' /tmp/speedprobe-py.json)
go_keys=$(jq -c '.[0] | del(.schema) | keys' /tmp/speedprobe-go.json)
echo
if [ "$py_keys" = "$go_keys" ]; then
  echo "OK: identical key sets (Go adds .schema)"
else
  echo "MISMATCH"; echo "python: $py_keys"; echo "go:     $go_keys"; exit 1
fi
jq -r '.[0] | [.latency_ms, .download_mbps, .upload_mbps, .loaded_latency_down_ms, .server] | @tsv' /tmp/speedprobe-py.json /tmp/speedprobe-go.json
