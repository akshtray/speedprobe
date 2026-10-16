---
status: accepted
date: 2026-10-16
---

# The engine emits events; renderers consume them; neither side imports the other

The measurement engine and the providers know nothing about terminals. They report progress as a stream of events (phase started, throughput sample, latency sample, run finished, run failed) on a channel. The plain renderer, the JSON writer and the Bubble Tea TUI are three consumers of that stream. The engine package never imports the TUI or output packages, and the TUI never calls a provider directly. We chose this over letting providers print progress because it is what makes the engine testable headlessly against the local test server, and because it lets the TUI and the plain renderer be developed independently of each other.

## Consequences

- Adding a new visual (a progress bar, a sparkline) is a change to a renderer only, never to a provider.
- A provider that needs to show something new adds an event type, and every renderer decides what to do with it.
