#!/usr/bin/env bash
# Runs the Python prototype and the Go port against the prototype's mock
# server with identical flags and compares the JSON they emit.
# Needs: uv, jq, go.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${PORT:-8765}
ARGS=(run -p cf,mlab -d "${DURATION:-3}" -c 2 --pings 5 --json -q)

uv run --with aiohttp python mock_server.py "$PORT" &
MOCK=$!
trap 'kill $MOCK 2>/dev/null || true' EXIT
for _ in $(seq 1 60); do
  curl -s -o /dev/null "http://127.0.0.1:$PORT/meta" && break
  sleep 0.5
done

export SPEEDPROBE_CLOUDFLARE_URL="http://127.0.0.1:$PORT"
export SPEEDPROBE_MLAB_LOCATE_URL="http://127.0.0.1:$PORT/locate"
echo ">> python"; uv run speedprobe.py "${ARGS[@]}" | tee /tmp/speedprobe-py.json
echo ">> go";     go run ./cmd/speedprobe "${ARGS[@]}" | tee /tmp/speedprobe-go.json

echo
rc=0
for i in 0 1; do
  py_keys=$(jq -c ".[$i] | keys" /tmp/speedprobe-py.json)
  go_keys=$(jq -c ".[$i] | del(.schema) | keys" /tmp/speedprobe-go.json)
  name=$(jq -r ".[$i].provider" /tmp/speedprobe-go.json)
  if [ "$py_keys" = "$go_keys" ]; then
    echo "OK: $name identical key sets (Go adds .schema)"
  else
    echo "MISMATCH: $name"; echo "python: $py_keys"; echo "go:     $go_keys"; rc=1
  fi
  jq -r ".[$i] | [.provider, .ok, .latency_ms, .jitter_ms, .download_mbps, .upload_mbps, .loaded_latency_down_ms, .loaded_latency_up_ms, .server] | @tsv" /tmp/speedprobe-py.json /tmp/speedprobe-go.json
done
exit $rc
