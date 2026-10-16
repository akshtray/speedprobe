"""Local stand-ins for Cloudflare's speed endpoints and an M-Lab ndt7 server (testing only)."""
import asyncio, json, os, sys, time
from aiohttp import web, WSMsgType

CHUNK = os.urandom(1 << 16)
RATE = 40 * 1024 * 1024 / 8 * 8  # throttle ~40 MB/s so numbers are stable


async def down(req):
    n = int(req.query.get("bytes", "0"))
    resp = web.StreamResponse(headers={"server-timing": "cfRequestDuration;dur=0.5", "cf-ray": "abc123-SEA"})
    resp.content_length = n
    await resp.prepare(req)
    sent = 0
    while sent < n:
        c = CHUNK[: min(len(CHUNK), n - sent)]
        await resp.write(c)
        sent += len(c)
        await asyncio.sleep(0.001)
    return resp


async def up(req):
    await req.read()
    return web.Response(text="ok")


async def meta(req):
    return web.json_response({"colo": "SEA", "clientIp": "127.0.0.1", "asOrganization": "Loopback"})


async def locate(req):
    host = req.host
    return web.json_response({"results": [{"machine": "mlab-mock", "location": {"city": "Localhost", "country": "US"},
                                           "urls": {"wss:///ndt/v7/download": f"ws://{host}/ndt/v7/download",
                                                    "wss:///ndt/v7/upload": f"ws://{host}/ndt/v7/upload"}}]})


def tcpinfo(t0, received=0):
    el = int((time.perf_counter() - t0) * 1e6)
    return json.dumps({"TCPInfo": {"MinRTT": 12000, "RTT": 25000, "RTTVar": 3000,
                                   "BytesReceived": received, "ElapsedTime": el}})


async def ndt_down(req):
    ws = web.WebSocketResponse(protocols=("net.measurementlab.ndt.v7",))
    await ws.prepare(req)
    t0 = time.perf_counter(); last = t0
    while time.perf_counter() - t0 < 3:
        await ws.send_bytes(CHUNK)
        await asyncio.sleep(0.002)
        if time.perf_counter() - last > 0.25:
            await ws.send_str(tcpinfo(t0)); last = time.perf_counter()
    await ws.close()
    return ws


async def ndt_up(req):
    ws = web.WebSocketResponse(protocols=("net.measurementlab.ndt.v7",), max_msg_size=0)
    await ws.prepare(req)
    t0 = time.perf_counter(); got = 0; last = t0
    async for msg in ws:
        if msg.type == WSMsgType.BINARY:
            got += len(msg.data)
        if time.perf_counter() - last > 0.25:
            await ws.send_str(tcpinfo(t0, got)); last = time.perf_counter()
    return ws


app = web.Application(client_max_size=64 * 1024 * 1024)
app.add_routes([web.get("/__down", down), web.post("/__up", up), web.get("/meta", meta),
                web.get("/locate", locate), web.get("/ndt/v7/download", ndt_down), web.get("/ndt/v7/upload", ndt_up)])
web.run_app(app, host="127.0.0.1", port=int(sys.argv[1]) if len(sys.argv) > 1 else 8765, print=None)
