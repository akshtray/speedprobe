#!/usr/bin/env python3
# /// script
# requires-python = ">=3.10"
# dependencies = ["aiohttp>=3.9", "rich>=13"]
# ///
"""speedprobe - run internet speed tests against several providers from one CLI.

Providers
  cloudflare  native   speed.cloudflare.com  (__down / __up endpoints)
  fast        native   Fast.com / Netflix Open Connect (unofficial API)
  mlab        native   Measurement Lab ndt7 over WebSocket
  ookla       wrapper  official Ookla `speedtest` binary (or python speedtest-cli)
  testmy      browser  TestMy.net has no public test API -> opens it in a browser

Run `speedprobe` with no arguments for an interactive picker, or
`speedprobe run -p cloudflare,mlab --json` for scripting.
"""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import re
import shutil
import statistics
import subprocess
import sys
import time
import webbrowser
from collections import deque
from contextlib import nullcontext
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from typing import Awaitable, Callable, Optional
from urllib.parse import urlsplit, urlunsplit

import aiohttp
from rich.console import Console
from rich.markup import escape
from rich.prompt import IntPrompt, Prompt
from rich.table import Table

__version__ = "0.1.0"
UA = f"speedprobe/{__version__}"
TESTS = ("latency", "download", "upload")

out = Console()
err = Console(stderr=True)

ProgressFn = Callable[[str], None]
ProbeFn = Callable[[], Awaitable[float]]


# --------------------------------------------------------------------------- #
# Data model
# --------------------------------------------------------------------------- #
@dataclass
class Options:
    tests: set[str] = field(default_factory=lambda: set(TESTS))
    duration: float = 10.0          # seconds per direction (HTTP providers)
    connections: int = 6            # parallel streams (HTTP providers)
    warmup: float = 1.5             # seconds excluded from throughput (TCP ramp-up)
    pings: int = 20                 # idle latency samples
    ookla_server_id: Optional[int] = None
    accept_ookla_license: bool = False
    mlab_server: int = 0            # index into M-Lab locate results


@dataclass
class Result:
    provider: str
    ok: bool = True
    server: Optional[str] = None
    latency_ms: Optional[float] = None
    jitter_ms: Optional[float] = None
    loaded_latency_down_ms: Optional[float] = None
    loaded_latency_up_ms: Optional[float] = None
    download_mbps: Optional[float] = None
    upload_mbps: Optional[float] = None
    packet_loss_pct: Optional[float] = None
    note: Optional[str] = None
    error: Optional[str] = None
    extra: dict = field(default_factory=dict)
    timestamp: str = field(
        default_factory=lambda: datetime.now(timezone.utc).isoformat(timespec="seconds")
    )

    def fail(self, phase: str, exc: BaseException) -> None:
        """Record a failed phase; identical errors across phases collapse to one entry."""
        self.ok = False
        self._failures.setdefault(describe(exc), []).append(phase)
        self.error = "; ".join(f"{'/'.join(ph)}: {msg}" for msg, ph in self._failures.items())

    def __post_init__(self) -> None:
        self._failures: dict[str, list[str]] = {}  # not a dataclass field -> stays out of JSON


def describe(exc: BaseException) -> str:
    if isinstance(exc, aiohttp.ClientResponseError):
        return f"HTTP {exc.status} {exc.message}".strip()
    if isinstance(exc, (asyncio.TimeoutError, TimeoutError)):
        return "timed out"
    text = str(exc).strip()
    return f"{type(exc).__name__}: {text}" if text else type(exc).__name__


def median(xs: list[float]) -> Optional[float]:
    return statistics.median(xs) if xs else None


def jitter(xs: list[float]) -> Optional[float]:
    """Mean absolute difference between consecutive samples (RFC 3550 style)."""
    if len(xs) < 2:
        return None
    return statistics.fmean(abs(a - b) for a, b in zip(xs, xs[1:]))


# --------------------------------------------------------------------------- #
# Measurement engine (shared by the HTTP providers)
# --------------------------------------------------------------------------- #
class Meter:
    """Counts bytes and reports a steady-state rate that skips the warm-up window."""

    def __init__(self, warmup: float):
        self.t0 = time.perf_counter()
        self.warmup = warmup
        self.total = 0
        self.t_last = self.t0
        self._warm: Optional[tuple[float, int]] = None
        self._hist: deque = deque(maxlen=8)

    def add(self, n: int) -> None:
        now = time.perf_counter()
        if self._warm is None and now - self.t0 >= self.warmup:
            self._warm = (now, self.total)
        self.total += n
        self.t_last = now

    def live_mbps(self) -> float:
        now = time.perf_counter()
        self._hist.append((now, self.total))
        (ta, ba), (tb, bb) = self._hist[0], self._hist[-1]
        return (bb - ba) * 8 / (tb - ta) / 1e6 if tb > ta else 0.0

    def mbps(self) -> float:
        if self._warm and self.t_last > self._warm[0]:
            tw, bw = self._warm
            return (self.total - bw) * 8 / (self.t_last - tw) / 1e6
        dt = self.t_last - self.t0
        return self.total * 8 / dt / 1e6 if dt > 0 else 0.0


async def _sleep_or_stop(stop: asyncio.Event, secs: float) -> None:
    try:
        await asyncio.wait_for(stop.wait(), secs)
    except asyncio.TimeoutError:
        pass


async def _ticker(meter: Meter, stop: asyncio.Event, progress: ProgressFn, label: str) -> None:
    while not stop.is_set():
        await _sleep_or_stop(stop, 0.25)
        progress(f"{label} {meter.live_mbps():9.1f} Mbps")


async def _loaded_pings(probe: Optional[ProbeFn], stop: asyncio.Event, sink: list[float]) -> None:
    """Latency under load = bufferbloat indicator."""
    if probe is None:
        return
    while not stop.is_set():
        try:
            sink.append(await probe())
        except Exception:
            pass
        await _sleep_or_stop(stop, 0.25)


WorkerFn = Callable[[int, Meter, float], Awaitable[None]]


async def transfer(label: str, worker: WorkerFn, opts: Options, progress: ProgressFn,
                   probe: Optional[ProbeFn] = None) -> tuple[float, Optional[float]]:
    """Run `opts.connections` workers for `opts.duration`; return (Mbps, loaded latency ms)."""
    meter = Meter(opts.warmup)
    deadline = meter.t0 + opts.duration
    stop = asyncio.Event()
    loaded: list[float] = []
    side = [asyncio.create_task(_ticker(meter, stop, progress, label)),
            asyncio.create_task(_loaded_pings(probe, stop, loaded))]
    try:
        results = await asyncio.gather(
            *(worker(i, meter, deadline) for i in range(opts.connections)),
            return_exceptions=True,
        )
    finally:
        stop.set()
        await asyncio.gather(*side, return_exceptions=True)
    errors = [r for r in results if isinstance(r, BaseException)]
    if meter.total == 0 and errors:
        raise errors[0]
    return meter.mbps(), median(loaded)


def http_download(session: aiohttp.ClientSession, url_for: Callable[[int], str]) -> WorkerFn:
    async def worker(i: int, meter: Meter, deadline: float) -> None:
        while time.perf_counter() < deadline:
            async with session.get(url_for(i)) as r:
                r.raise_for_status()
                async for chunk in r.content.iter_chunked(1 << 16):
                    meter.add(len(chunk))
                    if time.perf_counter() >= deadline:
                        r.close()  # drop the connection instead of draining the body
                        return
    return worker


_BLOB = b""


def _blob(n: int) -> memoryview:
    global _BLOB
    if len(_BLOB) < n:
        _BLOB = os.urandom(n)  # random = incompressible
    return memoryview(_BLOB)


def http_upload(session: aiohttp.ClientSession, url_for: Callable[[int, int], str], *,
                start: int = 256 * 1024, max_size: int = 16 * 1024 * 1024,
                headers: Optional[dict] = None) -> WorkerFn:
    """POST random payloads; payload size doubles while requests finish in < 1s."""
    async def worker(i: int, meter: Meter, deadline: float) -> None:
        size, blob = start, _blob(max_size)
        while time.perf_counter() < deadline:
            t = time.perf_counter()
            async with session.post(url_for(i, size), data=blob[:size], headers=headers) as r:
                await r.read()
                r.raise_for_status()
            meter.add(size)
            if time.perf_counter() - t < 1.0 and size < max_size:
                size = min(size * 2, max_size)
    return worker


async def idle_latency(probe: ProbeFn, n: int, progress: ProgressFn) -> tuple[Optional[float], Optional[float]]:
    await probe()  # absorb DNS + TCP + TLS setup so samples measure RTT only
    xs: list[float] = []
    for i in range(n):
        xs.append(await probe())
        progress(f"latency {i + 1}/{n}  {xs[-1]:.1f} ms")
    return median(xs), jitter(xs)


# --------------------------------------------------------------------------- #
# Providers
# --------------------------------------------------------------------------- #
class Provider:
    name = ""
    title = ""
    url = ""
    kind = "native"           # native | wrapper | browser
    aliases: tuple[str, ...] = ()
    blurb = ""

    def available(self) -> tuple[bool, str]:
        return True, "ready"

    async def run(self, session: aiohttp.ClientSession, opts: Options, progress: ProgressFn) -> Result:
        raise NotImplementedError


class Cloudflare(Provider):
    name, title, url = "cloudflare", "Cloudflare", "https://speed.cloudflare.com/"
    aliases = ("cf",)
    blurb = "latency, jitter, loaded latency (bufferbloat), down/up"

    def __init__(self) -> None:
        self.base = os.environ.get("SPEEDPROBE_CLOUDFLARE_URL", "https://speed.cloudflare.com").rstrip("/")
        self._colo: Optional[str] = None

    async def _probe(self, session: aiohttp.ClientSession) -> float:
        t = time.perf_counter()
        async with session.get(f"{self.base}/__down", params={"bytes": "0"}) as r:
            await r.read()
            timing = r.headers.get("server-timing", "")
            ray = r.headers.get("cf-ray", "")
        ms = (time.perf_counter() - t) * 1000
        m = re.search(r"dur=([\d.]+)", timing)  # subtract server-side processing time
        if m:
            ms = max(ms - float(m.group(1)), 0.0)
        if "-" in ray:
            self._colo = ray.rsplit("-", 1)[1]  # cf-ray ends with the PoP's IATA code
        return ms

    async def run(self, session, opts, progress) -> Result:
        res = Result(self.name)
        probe = lambda: self._probe(session)  # noqa: E731
        try:
            async with session.get(f"{self.base}/meta") as r:
                meta = await r.json(content_type=None)
            colo = meta.get("colo")
            if isinstance(colo, dict):
                colo = colo.get("iata") or colo.get("city")
            res.extra.update({k: meta[k] for k in ("clientIp", "asn", "asOrganization", "city", "country") if k in meta})
            if colo:
                self._colo = str(colo)
        except Exception:
            pass

        if "latency" in opts.tests:
            try:
                res.latency_ms, res.jitter_ms = await idle_latency(probe, opts.pings, progress)
            except Exception as e:
                res.fail("latency", e)
        if "download" in opts.tests:
            try:
                res.download_mbps, res.loaded_latency_down_ms = await transfer(
                    "download", http_download(session, lambda i: f"{self.base}/__down?bytes=25000000"),
                    opts, progress, probe)
            except Exception as e:
                res.fail("download", e)
        if "upload" in opts.tests:
            try:
                res.upload_mbps, res.loaded_latency_up_ms = await transfer(
                    "upload", http_upload(session, lambda i, n: f"{self.base}/__up",
                                          headers={"Content-Type": "text/plain;charset=UTF-8"}),
                    opts, progress, probe)
            except Exception as e:
                res.fail("upload", e)
        res.server = f"Cloudflare {self._colo}" if self._colo else "Cloudflare"
        return res


def _ranged(url: str, end: int) -> str:
    """Fast.com targets accept /range/0-N appended to the /speedtest path."""
    p = urlsplit(url)
    return urlunsplit(p._replace(path=p.path.rstrip("/") + f"/range/0-{end}"))


class FastCom(Provider):
    name, title, url = "fast", "Fast.com (Netflix)", "https://fast.com/"
    aliases = ("fastcom", "fast.com", "netflix")
    blurb = "Netflix Open Connect caches; unofficial API, upload best-effort"

    async def _targets(self, session: aiohttp.ClientSession) -> dict:
        async with session.get("https://fast.com/") as r:
            r.raise_for_status()
            html = await r.text()
        m = re.search(r'src="(/app-[^"]+\.js)"', html)
        if not m:
            raise RuntimeError("fast.com app script not found (page layout changed?)")
        async with session.get("https://fast.com" + m.group(1)) as r:
            js = await r.text()
        tok = re.search(r'token:"([A-Za-z0-9]+)"', js)
        if not tok:
            raise RuntimeError("fast.com API token not found (app script changed?)")
        params = {"https": "true", "token": tok.group(1), "urlCount": "5"}
        async with session.get("https://api.fast.com/netflix/speedtest/v2", params=params) as r:
            r.raise_for_status()
            return await r.json(content_type=None)

    async def run(self, session, opts, progress) -> Result:
        res = Result(self.name)
        progress("fetching Open Connect targets…")
        data = await self._targets(session)
        targets = [t["url"] for t in data.get("targets", []) if t.get("url")]
        if not targets:
            raise RuntimeError("fast.com API returned no targets")
        locs = {f"{t.get('location', {}).get('city', '?')}, {t.get('location', {}).get('country', '?')}"
                for t in data["targets"]}
        res.server = " | ".join(sorted(locs))
        client = data.get("client", {})
        res.extra.update({"isp": client.get("isp"), "asn": client.get("asn"), "targets": len(targets)})

        async def probe() -> float:
            t = time.perf_counter()
            async with session.get(_ranged(targets[0], 0)) as r:
                await r.read()
            return (time.perf_counter() - t) * 1000

        pick = lambda i: targets[i % len(targets)]  # noqa: E731
        if "latency" in opts.tests:
            try:
                res.latency_ms, res.jitter_ms = await idle_latency(probe, opts.pings, progress)
            except Exception as e:
                res.fail("latency", e)
        if "download" in opts.tests:
            try:
                res.download_mbps, res.loaded_latency_down_ms = await transfer(
                    "download", http_download(session, lambda i: _ranged(pick(i), 26214400)),
                    opts, progress, probe)
            except Exception as e:
                res.fail("download", e)
        if "upload" in opts.tests:
            try:
                res.upload_mbps, res.loaded_latency_up_ms = await transfer(
                    "upload", http_upload(session, lambda i, n: _ranged(pick(i), n - 1),
                                          headers={"Content-Type": "application/octet-stream"}),
                    opts, progress, probe)
            except Exception as e:
                res.fail("upload", e)
        return res


NDT7 = "net.measurementlab.ndt.v7"


class MLab(Provider):
    name, title, url = "mlab", "M-Lab (ndt7)", "https://www.measurementlab.net/"
    aliases = ("ndt", "ndt7", "measurementlab", "m-lab")
    blurb = "research-grade ndt7; ~10s per direction fixed by protocol, TCP_INFO RTTs"

    def __init__(self) -> None:
        self.locate = os.environ.get("SPEEDPROBE_MLAB_LOCATE_URL",
                                     "https://locate.measurementlab.net/v2/nearest/ndt/ndt7")

    @staticmethod
    def _tcpinfo(raw: str, rtt: dict) -> dict:
        try:
            tcp = json.loads(raw).get("TCPInfo") or {}
        except (ValueError, AttributeError):
            return {}
        if tcp.get("MinRTT"):
            rtt["min"].append(tcp["MinRTT"] / 1000)  # µs -> ms
        if tcp.get("RTTVar"):
            rtt["var"].append(tcp["RTTVar"] / 1000)
        if tcp.get("RTT"):
            rtt["cur"].append(tcp["RTT"] / 1000)
        return tcp

    async def _download(self, session, url, progress, rtt) -> float:
        total, t0 = 0, time.perf_counter()
        last = t0
        async with session.ws_connect(url, protocols=(NDT7,), max_msg_size=0) as ws:
            async for msg in ws:
                now = time.perf_counter()
                if msg.type == aiohttp.WSMsgType.BINARY:
                    total += len(msg.data)
                elif msg.type == aiohttp.WSMsgType.TEXT:
                    self._tcpinfo(msg.data, rtt)
                if now - last >= 0.25:
                    progress(f"download {total * 8 / (now - t0) / 1e6:9.1f} Mbps")
                    last = now
                if now - t0 > 15:  # protocol says ~10s; don't hang on a misbehaving server
                    break
        elapsed = time.perf_counter() - t0
        if total == 0:
            raise RuntimeError("server sent no data")
        return total * 8 / elapsed / 1e6

    async def _upload(self, session, url, progress, rtt) -> float:
        t0, sent, size = time.perf_counter(), 0, 1 << 13
        blob = _blob(1 << 20)
        server: dict = {}

        def rate() -> float:
            if server.get("ElapsedTime"):
                return server["BytesReceived"] * 8 / server["ElapsedTime"]  # bits/µs == Mbps
            return sent * 8 / max(time.perf_counter() - t0, 1e-6) / 1e6

        async with session.ws_connect(url, protocols=(NDT7,), max_msg_size=0) as ws:
            async def reader() -> None:
                async for msg in ws:
                    if msg.type == aiohttp.WSMsgType.TEXT:
                        tcp = self._tcpinfo(msg.data, rtt)
                        if tcp.get("BytesReceived") and tcp.get("ElapsedTime"):
                            server.update(tcp)

            rt = asyncio.create_task(reader())
            last = t0
            while (now := time.perf_counter()) - t0 < 10 and not ws.closed:
                await ws.send_bytes(blob[:size].tobytes())
                sent += size
                if size < len(blob) and sent >= 16 * size:  # ndt7 message scaling
                    size *= 2
                if now - last >= 0.25:
                    progress(f"upload {rate():9.1f} Mbps")
                    last = now
            await ws.close()
            try:
                await asyncio.wait_for(rt, 5)
            except Exception:
                rt.cancel()
        # Server-side TCP_INFO is authoritative for upload (client counts bytes buffered, not delivered).
        return rate()

    async def run(self, session, opts, progress) -> Result:
        res = Result(self.name)
        progress("asking locate.measurementlab.net for nearest server…")
        async with session.get(self.locate) as r:
            r.raise_for_status()
            data = await r.json(content_type=None)
        servers = data.get("results") or []
        if not servers:
            raise RuntimeError("locate API returned no servers")
        srv = servers[min(opts.mlab_server, len(servers) - 1)]
        loc = srv.get("location", {})
        res.server = f"{srv.get('machine', '?')} ({loc.get('city', '?')}, {loc.get('country', '?')})"
        res.extra["candidates"] = [s.get("machine") for s in servers]
        urls = srv.get("urls", {})

        down_rtt = {"min": [], "var": [], "cur": []}
        up_rtt = {"min": [], "var": [], "cur": []}
        # ndt7 has no standalone ping; latency comes from server TCP_INFO during a transfer.
        if {"download", "latency"} & opts.tests:
            try:
                mbps = await self._download(session, urls["wss:///ndt/v7/download"], progress, down_rtt)
                if "download" in opts.tests:
                    res.download_mbps = mbps
                    res.loaded_latency_down_ms = median(down_rtt["cur"])
            except Exception as e:
                res.fail("download", e)
        if "upload" in opts.tests:
            try:
                res.upload_mbps = await self._upload(session, urls["wss:///ndt/v7/upload"], progress, up_rtt)
                res.loaded_latency_up_ms = median(up_rtt["cur"])
            except Exception as e:
                res.fail("upload", e)
        mins = down_rtt["min"] + up_rtt["min"]
        if "latency" in opts.tests and mins:
            res.latency_ms = min(mins)
            res.jitter_ms = median(down_rtt["var"] + up_rtt["var"])
        return res


class Ookla(Provider):
    name, title, url = "ookla", "Ookla Speedtest", "https://www.speedtest.net/"
    kind = "wrapper"
    aliases = ("speedtest", "speedtest.net")
    blurb = "wraps the official `speedtest` binary (falls back to python speedtest-cli)"

    def __init__(self) -> None:
        self._det: Optional[tuple[Optional[str], Optional[str]]] = None

    def _detect(self) -> tuple[Optional[str], Optional[str]]:
        if self._det is None:
            exe, flavor = shutil.which("speedtest"), None
            if exe:
                try:
                    p = subprocess.run([exe, "--version"], capture_output=True, text=True,
                                       timeout=15, stdin=subprocess.DEVNULL)
                    txt = p.stdout + p.stderr
                    flavor = "ookla" if "Ookla" in txt else "speedtest-cli" if "speedtest-cli" in txt else "ookla"
                except Exception:
                    flavor = "ookla"
            self._det = (exe, flavor)
        return self._det

    def available(self) -> tuple[bool, str]:
        exe, flavor = self._detect()
        if not exe:
            return False, "install Ookla's `speedtest` CLI: https://www.speedtest.net/apps/cli"
        return True, f"{flavor} ({exe})"

    async def run(self, session, opts, progress) -> Result:
        exe, flavor = self._detect()
        assert exe
        res = Result(self.name)
        if flavor == "speedtest-cli":
            cmd = [exe, "--json", "--secure"]
            if "download" not in opts.tests:
                cmd.append("--no-download")
            if "upload" not in opts.tests:
                cmd.append("--no-upload")
            if opts.ookla_server_id:
                cmd += ["--server", str(opts.ookla_server_id)]
        else:
            cmd = [exe, "--format=json", "--progress=no"]
            if opts.accept_ookla_license:
                cmd += ["--accept-license", "--accept-gdpr"]
            if opts.ookla_server_id:
                cmd += ["--server-id", str(opts.ookla_server_id)]
            if opts.tests != set(TESTS):
                res.note = "Ookla CLI always runs every test"

        progress(f"running {os.path.basename(exe)} (~30s)…")
        proc = await asyncio.create_subprocess_exec(
            *cmd, stdin=asyncio.subprocess.DEVNULL,
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
        try:
            so, se = await asyncio.wait_for(proc.communicate(), 180)
        except asyncio.TimeoutError:
            proc.kill()
            raise RuntimeError("speedtest binary timed out after 180s")
        stdout, stderr = so.decode(errors="replace"), se.decode(errors="replace")

        if flavor == "speedtest-cli":
            try:
                d = json.loads(stdout)
            except ValueError:
                raise RuntimeError((stderr or stdout).strip().splitlines()[-1] if (stderr or stdout).strip()
                                   else f"exit {proc.returncode}")
            s = d.get("server", {})
            res.server = f"{s.get('sponsor', '?')} ({s.get('name', '?')}, {s.get('country', '?')}) #{s.get('id', '?')}"
            res.latency_ms = d.get("ping")
            res.download_mbps = d["download"] / 1e6 if d.get("download") else None
            res.upload_mbps = d["upload"] / 1e6 if d.get("upload") else None
            res.extra["share"] = d.get("share")
            return res

        result, msgs = None, []
        for line in (stdout + "\n" + stderr).splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except ValueError:
                msgs.append(line)
                continue
            if obj.get("type") == "result":
                result = obj
            elif obj.get("message"):
                msgs.append(str(obj["message"]))
        if result is None:
            blob = " ".join(msgs).lower()
            hint = msgs[-1] if msgs else f"exit code {proc.returncode}"
            if "license" in blob or "gdpr" in blob:
                hint = ("Ookla license not accepted - run `speedtest` once interactively, "
                        "or pass --accept-ookla-license")
            raise RuntimeError(hint)

        ping, dl, ul, s = result.get("ping", {}), result.get("download", {}), result.get("upload", {}), result.get("server", {})
        res.server = f"{s.get('name', '?')} ({s.get('location', '?')}) #{s.get('id', '?')}"
        res.latency_ms, res.jitter_ms = ping.get("latency"), ping.get("jitter")
        res.download_mbps = dl["bandwidth"] * 8 / 1e6 if dl.get("bandwidth") else None
        res.upload_mbps = ul["bandwidth"] * 8 / 1e6 if ul.get("bandwidth") else None
        res.loaded_latency_down_ms = (dl.get("latency") or {}).get("iqm")
        res.loaded_latency_up_ms = (ul.get("latency") or {}).get("iqm")
        res.packet_loss_pct = result.get("packetLoss")
        res.extra.update({"isp": result.get("isp"), "result_url": (result.get("result") or {}).get("url")})
        return res


class TestMy(Provider):
    name, title, url = "testmy", "TestMy.net", "https://testmy.net/"
    kind = "browser"
    aliases = ("testmy.net",)
    blurb = "no public test API - opens the site in your browser"

    async def run(self, session, opts, progress) -> Result:
        opened = webbrowser.open(self.url)
        return Result(self.name, server="browser",
                      note="opened in browser" if opened else f"open {self.url} manually")


PROVIDERS: list[Provider] = [Cloudflare(), FastCom(), MLab(), Ookla(), TestMy()]


def resolve(tokens: list[str]) -> list[Provider]:
    picked: list[Provider] = []
    for tok in (t.strip().lower() for t in tokens):
        if not tok:
            continue
        if tok in ("a", "all"):
            cands = [p for p in PROVIDERS if p.kind != "browser"]
        elif tok.isdigit() and 1 <= int(tok) <= len(PROVIDERS):
            cands = [PROVIDERS[int(tok) - 1]]
        else:
            cands = [p for p in PROVIDERS if tok == p.name or tok in p.aliases]
            if not cands:
                names = ", ".join(p.name for p in PROVIDERS)
                raise SystemExit(f"speedprobe: unknown provider '{tok}' (choose from: {names})")
        picked += [p for p in cands if p not in picked]
    return picked


def parse_tests(spec: str) -> set[str]:
    items = {t.strip().lower() for t in spec.split(",") if t.strip()}
    if not items or "all" in items:
        return set(TESTS)
    bad = items - set(TESTS)
    if bad:
        raise SystemExit(f"speedprobe: unknown test(s) {sorted(bad)} (choose from: {', '.join(TESTS)})")
    return items


# --------------------------------------------------------------------------- #
# Runner + output
# --------------------------------------------------------------------------- #
async def run_providers(providers: list[Provider], opts: Options, quiet: bool) -> list[Result]:
    results: list[Result] = []
    timeout = aiohttp.ClientTimeout(total=None, sock_connect=15, sock_read=30)
    connector = aiohttp.TCPConnector(limit=0, ttl_dns_cache=300)
    async with aiohttp.ClientSession(timeout=timeout, connector=connector, trust_env=True,
                                     headers={"User-Agent": UA}) as session:
        for p in providers:  # sequential on purpose: concurrent tests would starve each other
            ok, why = p.available()
            if not ok:
                results.append(Result(p.name, ok=False, error=why))
                continue
            status = nullcontext() if quiet else err.status(f"[bold]{p.title}[/] starting…")
            with status as st:
                def progress(msg: str, _st=st, _t=p.title) -> None:
                    if _st is not None:
                        _st.update(f"[bold]{escape(_t)}[/] {escape(msg)}")
                try:
                    r = await asyncio.wait_for(p.run(session, opts, progress),
                                               timeout=opts.duration * 2 + 180)
                except Exception as e:
                    r = Result(p.name, ok=False, error=describe(e))
            results.append(r)
            if not quiet:
                err.print(f"[green]✓[/] {p.title}" if r.ok else f"[red]✗[/] {p.title}")
    return results


def _f(v: Optional[float], unit: str) -> str:
    return "—" if v is None else f"{v:,.1f} {unit}"


def render(results: list[Result]) -> None:
    t = Table(title=f"speedprobe · {datetime.now().astimezone():%Y-%m-%d %H:%M %Z}", title_style="bold")
    for col, kw in [("Provider", {"style": "bold"}), ("Server", {"overflow": "fold", "max_width": 34}),
                    ("Ping", {"justify": "right"}), ("Jitter", {"justify": "right"}),
                    ("Loaded ↓ / ↑", {"justify": "right"}), ("Download", {"justify": "right", "style": "cyan"}),
                    ("Upload", {"justify": "right", "style": "magenta"}), ("Notes", {"overflow": "fold"})]:
        t.add_column(col, **kw)
    for r in results:
        loaded = "—"
        if r.loaded_latency_down_ms is not None or r.loaded_latency_up_ms is not None:
            dn = "—" if r.loaded_latency_down_ms is None else f"{r.loaded_latency_down_ms:.0f}"
            up = "—" if r.loaded_latency_up_ms is None else f"{r.loaded_latency_up_ms:.0f}"
            loaded = f"{dn} / {up} ms"
        notes = []
        if r.error:
            notes.append(f"[red]{escape(r.error)}[/]")
        if r.note:
            notes.append(escape(r.note))
        if r.packet_loss_pct is not None:
            notes.append(f"loss {r.packet_loss_pct:.1f}%")
        if r.extra.get("result_url"):
            notes.append(escape(r.extra["result_url"]))
        name = next((p.title for p in PROVIDERS if p.name == r.provider), r.provider)
        t.add_row(name, escape(r.server or "—"), _f(r.latency_ms, "ms"), _f(r.jitter_ms, "ms"), loaded,
                  _f(r.download_mbps, "Mbps"), _f(r.upload_mbps, "Mbps"), "\n".join(notes))
    out.print(t)


def _record(r: Result) -> dict:
    return {k: round(v, 2) if isinstance(v, float) else v for k, v in asdict(r).items()}


def execute(providers: list[Provider], opts: Options, *, as_json: bool = False, log: Optional[str] = None,
            repeat: int = 1, interval: float = 300, quiet: bool = False) -> int:
    rc, rnd = 0, 0
    while True:
        rnd += 1
        results = asyncio.run(run_providers(providers, opts, quiet))
        if as_json:
            payload = [_record(r) for r in results]
            print(json.dumps(payload, indent=None if repeat != 1 else 2), flush=True)
        else:
            render(results)
        if log:
            with open(log, "a", encoding="utf-8") as fh:
                for r in results:
                    fh.write(json.dumps(_record(r)) + "\n")
        if any(not r.ok for r in results):
            rc = 1
        if repeat and rnd >= repeat:
            return rc
        if not quiet:
            err.print(f"[dim]round {rnd} done; next in {interval:.0f}s (Ctrl-C to stop)[/]")
        time.sleep(interval)


# --------------------------------------------------------------------------- #
# CLI
# --------------------------------------------------------------------------- #
def cmd_list(numbered: bool = False) -> int:
    t = Table(title="Providers", title_style="bold")
    if numbered:
        t.add_column("#", justify="right")
    for col in ("Name", "Aliases", "Type", "Status", "What it measures"):
        t.add_column(col, overflow="fold")
    for i, p in enumerate(PROVIDERS, 1):
        ok, why = p.available()
        row = [p.name, ", ".join(p.aliases), p.kind,
               f"[green]{escape(why)}[/]" if ok else f"[yellow]{escape(why)}[/]", p.blurb]
        t.add_row(*([str(i)] if numbered else []), *row)
    (err if numbered else out).print(t)
    return 0


def cmd_interactive() -> int:
    cmd_list(numbered=True)
    pick = Prompt.ask("Providers [dim](numbers/names, comma-separated; a = all CLI providers)[/]",
                      default="1", console=err)
    providers = resolve(pick.replace(" ", ",").split(","))
    tests = parse_tests(Prompt.ask("Tests", default="latency,download,upload", console=err))
    opts = Options(tests=tests)
    if any(p.kind == "native" and p.name != "mlab" for p in providers) and ({"download", "upload"} & tests):
        opts.duration = IntPrompt.ask("Seconds per direction", default=10, console=err)
        opts.connections = IntPrompt.ask("Parallel connections", default=6, console=err)
    return execute(providers, opts)


def build_parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser(
        prog="speedprobe",
        description="Multi-provider internet speed test. No arguments = interactive picker.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="examples:\n"
               "  speedprobe list\n"
               "  speedprobe run                          # Cloudflare, all tests\n"
               "  speedprobe run -p cf,mlab,fast -t download,upload\n"
               "  speedprobe run --all --json > result.json\n"
               "  speedprobe run -p cf --repeat 0 --interval 600 --log ~/speed.jsonl -q",
    )
    ap.add_argument("--version", action="version", version=f"%(prog)s {__version__}")
    sub = ap.add_subparsers(dest="cmd")
    sub.add_parser("list", help="show providers and whether they're usable on this machine")

    r = sub.add_parser("run", help="run speed tests")
    r.add_argument("-p", "--provider", action="append", default=[], metavar="NAME",
                   help="provider(s), repeatable or comma-separated (default: cloudflare)")
    r.add_argument("-a", "--all", action="store_true", help="all CLI-capable providers (skips browser-only)")
    r.add_argument("-t", "--tests", default="latency,download,upload",
                   help="comma-separated subset of: latency,download,upload (default: all)")
    r.add_argument("-d", "--duration", type=float, default=10.0, help="seconds per direction, HTTP providers (default 10)")
    r.add_argument("-c", "--connections", type=int, default=6, help="parallel streams, HTTP providers (default 6)")
    r.add_argument("--pings", type=int, default=20, help="idle latency samples (default 20)")
    r.add_argument("--json", action="store_true", help="JSON to stdout instead of a table")
    r.add_argument("--log", metavar="FILE", help="append each result as a JSON line to FILE")
    r.add_argument("--repeat", type=int, default=1, help="rounds to run; 0 = until Ctrl-C (default 1)")
    r.add_argument("--interval", type=float, default=300, help="seconds between rounds (default 300)")
    r.add_argument("-q", "--quiet", action="store_true", help="no progress output on stderr")
    g = r.add_argument_group("provider-specific")
    g.add_argument("--ookla-server-id", type=int, help="pin an Ookla server (see `speedtest -L`)")
    g.add_argument("--accept-ookla-license", action="store_true",
                   help="pass --accept-license --accept-gdpr to the Ookla binary")
    g.add_argument("--mlab-server", type=int, default=0,
                   help="index into M-Lab's nearest-server list (0 = closest)")
    return ap


def main(argv: Optional[list[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        if args.cmd == "list":
            return cmd_list()
        if args.cmd == "run":
            tokens = [t for chunk in args.provider for t in chunk.split(",")]
            providers = resolve((["all"] if args.all else []) + tokens) or [PROVIDERS[0]]
            opts = Options(tests=parse_tests(args.tests), duration=args.duration,
                           connections=max(1, args.connections), pings=max(2, args.pings),
                           ookla_server_id=args.ookla_server_id,
                           accept_ookla_license=args.accept_ookla_license, mlab_server=args.mlab_server)
            return execute(providers, opts, as_json=args.json, log=args.log, repeat=args.repeat,
                           interval=args.interval, quiet=args.quiet)
        if not sys.stdin.isatty():
            parser.print_help()
            return 2
        return cmd_interactive()
    except KeyboardInterrupt:
        err.print("\n[yellow]interrupted[/]")
        return 130


if __name__ == "__main__":
    sys.exit(main())
