# speedprobe

One tool that measures your internet connection against several independent speed-test providers, so you can compare them instead of trusting one vendor. A single static binary for Linux, macOS and Windows, with an interactive dashboard for people and flags plus JSON for scripts.

Why it exists: Ookla's own install script fails on current Ubuntu (24.04) because their apt repository does not publish it, the Python `speedtest-cli` package has been unmaintained since 2021, and every other speed-test CLI measures against exactly one vendor.

| Provider | How | Measures |
|---|---|---|
| `cloudflare` (`cf`) | Native HTTP against `speed.cloudflare.com` | idle latency, jitter, loaded latency (bufferbloat), download, upload |
| `mlab` (`ndt7`) | Native ndt7 via M-Lab's official client; nearest server from the Locate API | min RTT, RTT variance, loaded RTT (server TCP_INFO), download, upload |
| `ookla` (`speedtest`) | Wraps Ookla's official `speedtest` binary, which you install separately | latency, jitter, loaded latency, packet loss, download, upload, result URL |
| `fast` (`netflix`), experimental | Native HTTP against Netflix Open Connect caches via fast.com's undocumented API | latency, jitter, loaded latency, download |

**Data policy:** M-Lab publishes every test, including your IP address, as an open dataset ([their privacy page](https://www.measurementlab.net/privacy/)). Ookla stores results on speedtest.net. speedprobe itself makes no network connections other than to the providers you select: no automatic update checks, no telemetry. `speedprobe version --check` contacts GitHub only when you run it.

## Install

**Homebrew** (macOS and Linux), with upgrades via `brew upgrade`:

```bash
brew install akshtray/tap/speedprobe
```

**Debian and Ubuntu.** The URL below always points at the newest release; run the same two lines again to upgrade. Use `linux_arm64` on 64-bit ARM (Raspberry Pi 4/5, ARM servers) or `linux_armv7` on older Pis.

```bash
curl -LO https://github.com/akshtray/speedprobe/releases/latest/download/speedprobe_linux_amd64.deb
sudo apt install ./speedprobe_linux_amd64.deb
```

**Fedora, RHEL and friends:**

```bash
curl -LO https://github.com/akshtray/speedprobe/releases/latest/download/speedprobe_linux_amd64.rpm
sudo dnf install ./speedprobe_linux_amd64.rpm
```

**Windows.** Download `speedprobe_windows_amd64.zip` (or `windows_arm64`) from the [latest release](https://github.com/akshtray/speedprobe/releases/latest), extract `speedprobe.exe`, and run it from a terminal or put its folder on `PATH`. The binary is not code-signed, so SmartScreen may ask once; "More info, Run anyway" clears it.

**Any other system:** the matching `.tar.gz` from the latest release (Linux, macOS, FreeBSD), or with Go installed:

```bash
go install github.com/akshtray/speedprobe/cmd/speedprobe@latest
```

**Staying current.** speedprobe never checks for updates on its own. When you want to know, ask:

```bash
speedprobe version --check     # installed vs latest, and the upgrade command for this install
```

To verify a download, check `checksums.txt` against the archive and the Sigstore signature on the checksums file:

```bash
sha256sum --check --ignore-missing checksums.txt
cosign verify-blob --certificate checksums.txt.pem --signature checksums.txt.sig \
  --certificate-identity-regexp 'github.com/akshtray/speedprobe' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
```

Optional, for the Ookla provider: install their CLI from <https://www.speedtest.net/apps/cli>. On Ubuntu 24.04 use the standalone tarball from <https://install.speedtest.net/app/cli/>, since their apt repository has no `noble` release. Run `speedtest` once to accept Ookla's license, or pass `--accept-ookla-license`.

## Usage

```bash
speedprobe                          # interactive dashboard (on a terminal)
speedprobe list                     # providers and whether each is usable here
speedprobe run                      # Cloudflare, all measurements, plain output
speedprobe run -p cf,mlab,ookla     # several providers, one after another
speedprobe run --all                # every provider
speedprobe run -p cf -t download    # subset of latency,download,upload
speedprobe run -p cf -d 20 -c 8     # 20 s per direction, 8 parallel streams
speedprobe run -p mlab --mlab-server 1          # second-closest M-Lab server
speedprobe run -p ookla --ookla-server-id 12345 --accept-ookla-license
```

The dashboard opens when both stdin and stdout are a terminal. `speedprobe run` never opens it, so cron jobs are safe, and a piped bare `speedprobe` behaves like `run`.

### Scripting

```bash
# JSON to stdout; exit code 1 if any provider failed
speedprobe run -p cf,mlab --json -q | jq '.[] | {provider, download_mbps, upload_mbps}'

# Every 15 minutes until Ctrl-C, one JSON line per result appended to a log
speedprobe run -p cf --repeat 0 --interval 900 --log ~/speedprobe.jsonl -q

jq -r '[.timestamp, .provider, .download_mbps, .upload_mbps, .loaded_latency_down_ms] | @tsv' ~/speedprobe.jsonl
```

Progress goes to stderr and results to stdout. Exit codes: 0 success, 1 at least one provider failed, 2 usage error, 130 interrupted.

### JSON

Each result is one object with these fields, always present, `null` when not measured:

`provider`, `ok`, `server`, `latency_ms`, `jitter_ms`, `loaded_latency_down_ms`, `loaded_latency_up_ms`, `download_mbps`, `upload_mbps`, `packet_loss_pct`, `note`, `error`, `extra`, `timestamp`, `schema`

Floats are rounded to two decimals. `extra` holds provider-specific details (Cloudflare colo and client IP, M-Lab candidate servers and test UUID, Ookla ISP and result URL). Within a major version fields are only ever added, never renamed or removed; `schema` is bumped if that promise is broken.

## How the numbers are computed

- **Throughput (Cloudflare, Fast.com):** `-c` parallel HTTP/1.1 streams for `-d` seconds. The first 1.5 s is excluded so TCP slow-start does not drag the average down. Uploads send random, incompressible payloads that double in size while requests finish in under a second, up to 16 MB.
- **Idle latency:** one warm-up request to absorb DNS, TCP and TLS, then `--pings` sequential tiny requests on the kept-alive connection. Reported as the median; jitter is the mean absolute difference between consecutive samples. Cloudflare's server-side processing time is subtracted.
- **Loaded latency:** the same probe every 250 ms *while* the transfer runs. The gap between idle and loaded latency is your bufferbloat; the dashboard draws it live.
- **M-Lab:** the ndt7 protocol fixes about 10 s per direction. Latency and jitter come from the server's kernel TCP_INFO (minimum RTT and RTT variance), loaded latency is the median RTT during each transfer, and upload uses the server's byte count because the client only knows what it buffered.
- **Ookla:** whatever the binary reports; it always runs every measurement, so `-t` does not apply.
- A measurement a provider cannot supply is skipped, left `null`, and explained in `note`.

Because the methods differ, expect providers to disagree somewhat. Same-provider trends over time mean more than cross-provider comparisons.

## Caveats

- **Fast.com** depends on an undocumented API. If Netflix changes the page, the provider fails with a clear error until it is updated; please report it.
- **Ookla** is only wrapped, never bundled. speedprobe never downloads their binary.
- **Proxies:** `HTTP(S)_PROXY` and `NO_PROXY` are honoured, which means you measure the proxy path, not your raw link.

## Development

Everything runs offline against local stand-ins for each provider:

```bash
go test -race ./...
go run ./cmd/speedprobe run -p cf -d 3
```

Environment overrides point a provider at a local server: `SPEEDPROBE_CLOUDFLARE_URL`, `SPEEDPROBE_MLAB_LOCATE_URL`, `SPEEDPROBE_FAST_URL`, `SPEEDPROBE_FAST_API_URL`, `SPEEDPROBE_OOKLA_BIN`. See [internal/testserver](internal/testserver).

Vocabulary is in [CONTEXT.md](CONTEXT.md); decisions are in [docs/adr](docs/adr). The original Python prototype is preserved at the `python-v0.1.0` tag. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT.
