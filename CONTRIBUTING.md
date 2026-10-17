# Contributing

Thanks for looking. speedprobe is small on purpose; the fastest way to help is a focused pull request from your fork.

## Before you start

- Read [CONTEXT.md](CONTEXT.md). It is the glossary: a *provider* is a service, a *measurement* is latency, download or upload, a *run* is one provider's execution. Code and docs use those words.
- Read [docs/adr/](docs/adr/) for the decisions you should not accidentally undo: the JSON output is a frozen contract, and the engine never imports a renderer.

## Developing

```bash
go test -race ./...       # everything runs against a local test server; no internet needed
go vet ./...
go run ./cmd/speedprobe run -p cf -d 3
```

Set `SPEEDPROBE_CLOUDFLARE_URL` to point a run at a local server (see `internal/testserver`).

## Pull requests

- Use [conventional commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:` ...). Release notes are generated from them.
- Add or update a test. Provider code must be testable against `internal/testserver`.
- Keep the JSON field names. Adding a field is fine; renaming or removing one is a major version.
- A new provider needs a `DataPolicy` line if the service publishes or retains test data.

## Reporting a provider breakage

Use the "A provider stopped working" issue template and include `--json` output. Fast.com relies on an undocumented API and will break occasionally; those reports are still welcome.
