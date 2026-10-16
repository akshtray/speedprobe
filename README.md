# speedprobe

One CLI for internet speed tests across several providers, with an interactive picker for ad-hoc use and flags + JSON for scripting.

| Provider | How it runs | Measures |
|---|---|---|
| `cloudflare` (`cf`) | Native HTTP against `speed.cloudflare.com/__down` and `/__up` | idle latency, jitter, loaded latency (bufferbloat), down, up |
| `fast` (`netflix`) | Native — scrapes fast.com's API token, then pulls from Netflix Open Connect caches | latency, jitter, loaded latency, down; upload is best-effort |
| `mlab` (`ndt7`) | Native ndt7 over WebSocket via `locate.measurementlab.net` | min RTT, RTT variance, loaded RTT (server TCP_INFO), down, up |
| `ookla` (`speedtest`) | Wraps the official Ookla `speedtest` binary (or python `speedtest-cli` if that's what's on PATH) | latency, jitter, loaded latency, packet loss, down, up, result URL |
| `testmy` | Opens testmy.net in your browser — it has no public test API | — |

## Install

Needs Python 3.10+. Pick one:

```bash
# Run directly; uv reads the inline deps from the script header
uv run speedprobe.py

# Or install as a command on PATH
uv tool install .        # or: pipx install .
speedprobe --version
```

Optional, for the Ookla provider:

```bash
brew install teamookla/speedtest/speedtest     # macOS
# Linux/Windows: https://www.speedtest.net/apps/cli
speedtest                                      # run once to accept Ookla's license
```

## Usage

```bash
speedprobe                         # interactive: pick providers, tests, duration
speedprobe list                    # providers + whether each is usable on this machine
speedprobe run                     # Cloudflare, all tests
speedprobe run -p cf,mlab,fast     # several providers, run one after another
speedprobe run --all               # every CLI-capable provider (skips testmy)
speedprobe run -p cf -t download   # subset of: latency,download,upload
speedprobe run -p cf -d 20 -c 8    # 20s per direction, 8 parallel streams
speedprobe run -p ookla --ookla-server-id 12345 --accept-ookla-license
speedprobe run -p mlab --mlab-server 1   # second-closest M-Lab server
```

### Scripting / monitoring

```bash
export LOG=~/speedprobe.jsonl
export INTERVAL=900

# JSON to stdout (floats rounded to 2 dp); exit code 1 if any provider failed
speedprobe run -p cf,mlab --json -q | jq '.[] | {provider, download_mbps, upload_mbps}'

# Run every $INTERVAL seconds until Ctrl-C, appending one JSON line per result
speedprobe run -p cf --repeat 0 --interval $INTERVAL --log $LOG -q

# Then look at the history
jq -r '[.timestamp, .provider, .download_mbps, .upload_mbps, .loaded_latency_down_ms] | @tsv' $LOG
```

Progress goes to stderr and results to stdout, so piping works cleanly.

## How the numbers are computed

- **Throughput (Cloudflare, Fast.com):** `-c` parallel streams for `-d` seconds. The first 1.5 s is excluded so TCP slow-start doesn't drag the average down. Uploads use random (incompressible) payloads that double in size while requests finish in under a second, up to 16 MB.
- **Idle latency:** one warm-up request to absorb DNS/TCP/TLS, then `--pings` sequential tiny requests on the kept-alive connection. Reported as the median; jitter is the mean absolute difference between consecutive samples. Cloudflare's `server-timing` processing time is subtracted.
- **Loaded latency:** the same probe fired every 250 ms *while* the transfer runs. The gap between idle and loaded latency is your bufferbloat.
- **M-Lab:** follows ndt7 rather than the engine above. Duration is fixed at about 10 s per direction by the server, latency/jitter come from the server's kernel TCP_INFO (`MinRTT`, `RTTVar`), and upload uses the server's `BytesReceived` because the client can only see what it buffered.
- **Ookla:** whatever the binary reports. It always runs every test, so `-t` doesn't apply.

Because the methods differ, expect providers to disagree somewhat. Same-provider trends over time are more meaningful than cross-provider comparisons.

## Caveats

- **Fast.com** uses an undocumented API; if Netflix changes the page, token extraction will fail with a clear error. Upload works by POSTing to the cache URLs, which isn't guaranteed.
- **Proxies:** `HTTP(S)_PROXY` / `NO_PROXY` are honored. Note you'll then be measuring the proxy path, not your raw link.
- **TestMy.net** only offers undocumented browser-side upload endpoints, so it's a browser launcher rather than a fragile scrape.

## Testing without the internet

`mock_server.py` serves stand-ins for Cloudflare's endpoints and an ndt7 server:

```bash
export PORT=8765
python mock_server.py $PORT &
export SPEEDPROBE_CLOUDFLARE_URL=http://127.0.0.1:$PORT
export SPEEDPROBE_MLAB_LOCATE_URL=http://127.0.0.1:$PORT/locate
speedprobe run -p cf,mlab -d 3
```
