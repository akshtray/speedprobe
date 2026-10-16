---
status: accepted
date: 2026-10-16
---

# Rewrite in Go rather than packaging the Python prototype

The working prototype is a single Python file (`speedprobe.py`) built on aiohttp and rich. The goal is a tool anyone can install on Linux, macOS and Windows without a runtime, prompted by Ookla's own install script failing on Ubuntu 24.04. We are rewriting in Go because it cross-compiles to static binaries of a few MB for every target from one machine, GoReleaser covers the whole release pipeline, Bubble Tea is a mature TUI library, and M-Lab maintains its official ndt7 reference client in Go.

## Considered Options

- **Package the Python with PyInstaller or Nuitka.** Fastest route but produces 30-60 MB binaries, slow startup, and frequent Windows antivirus false positives.
- **Rust with Ratatui.** Excellent TUI story and the best third-party Cloudflare client is Rust, but ndt7 would have to be hand-rolled (the only crate is a 4-star personal project) and cross-compiling to macOS and Windows is harder than Go.

## Consequences

- The Python prototype becomes the reference for behaviour and the JSON field names, then is retired.
- Cloudflare's protocol is simple HTTP, so the lack of a strong Go library there is cheap to absorb.
