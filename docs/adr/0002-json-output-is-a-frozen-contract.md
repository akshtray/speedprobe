---
status: accepted
date: 2026-10-16
---

# JSON output keeps the Python prototype's field names and only ever grows

The Python prototype emitted one JSON object per result with the fields `provider`, `ok`, `server`, `latency_ms`, `jitter_ms`, `loaded_latency_down_ms`, `loaded_latency_up_ms`, `download_mbps`, `upload_mbps`, `packet_loss_pct`, `note`, `error`, `extra` and `timestamp`. The Go rewrite keeps every one of those names unchanged, adds `"schema": 1` to every object and every JSONL log line, and promises that within a major version fields are only added, never renamed, removed or retyped. We chose this over a cleaner nested schema because `--json` and `--log` are the scripting interface, and a stable flat object is worth more to a `jq` user than tidier naming is to us.

## Consequences

- Go struct field tags are dictated by this list, not by Go naming taste.
- A measurement a provider cannot supply is `null`, not absent, so consumers never need to test for key presence.
- Breaking the schema requires bumping `schema` and the major version together.
