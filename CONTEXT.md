# speedprobe

A single tool that measures an internet connection against several independent speed-test services so the numbers can be compared and no single vendor is trusted blindly.

## Language

**Provider**:
An external speed-test service that speedprobe can measure against, such as Cloudflare or M-Lab. Each provider has a data policy the user should know; M-Lab in particular publishes every test, including the client IP, as an open dataset.
_Avoid_: backend, server, service, vendor

**Capability**:
The set of measurements a provider can supply. A measurement requested from a provider that lacks the capability is skipped, reported as null, and explained in the result's note.
_Avoid_: feature, support matrix

**Measurement**:
One of the three quantities a provider can be asked for: latency, download, or upload.
_Avoid_: test, check, metric

**Run**:
The execution of a chosen set of measurements against one provider, producing exactly one result.
_Avoid_: test, speed test, probe

**Result**:
The outcome of one run: the measured values, the provider's server, a timestamp, and either success or the error that stopped it.
_Avoid_: report, reading, record

**Session**:
One invocation of speedprobe. It contains one or more runs, across several providers or repeated over time. Within a session at most one transfer is in progress at any moment; runs never overlap, because concurrent transfers would share the link and both results would be wrong.
_Avoid_: job, batch

**Idle latency**:
Round-trip time measured while no transfer is in progress.
_Avoid_: ping, base latency

**Loaded latency**:
Round-trip time measured while a download or upload is in progress. The gap between idle and loaded latency is bufferbloat.
_Avoid_: latency under load, bufferbloat (which names the gap, not the measurement)

**Jitter**:
Mean absolute difference between consecutive latency samples.
_Avoid_: variance, latency spread

**Native provider**:
A provider that speedprobe speaks to directly over the network using its protocol.
_Avoid_: built-in, first-class

**Wrapped provider**:
A provider measured by invoking a separately installed third-party binary and parsing its output. Ookla is the only one.
_Avoid_: external, plugin

**Experimental provider**:
A provider that depends on an undocumented interface its operator could change without notice. Fast.com is the only one. Its results are real but its continued operation is not promised.
_Avoid_: unofficial, beta, unstable
