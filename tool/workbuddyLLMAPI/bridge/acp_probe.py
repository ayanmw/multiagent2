#!/usr/bin/env python3
"""ACP probe v4: deltas arrive on the persistent GET SSE stream, not the POST body."""
import json
import os
import sys
import time
import threading
import urllib.request
import urllib.error

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18765"
HDR = {"Content-Type": "application/json", "X-CodeBuddy-Request": "1"}


def post_raw(path, body, extra=None, stream=False):
    data = json.dumps(body).encode("utf-8")
    h = dict(HDR)
    if extra:
        h.update(extra)
    req = urllib.request.Request(BASE + path, data=data, headers=h, method="POST")
    r = urllib.request.urlopen(req, timeout=90)
    if stream:
        return r  # caller iterates the streaming body
    raw = r.read().decode("utf-8", "replace")
    return raw


def parse_sse_blob(text):
    out = []
    for line in text.splitlines():
        if line.startswith("data:"):
            p = line[5:].strip()
            try:
                out.append(json.loads(p))
            except Exception:
                pass
    return out


def main():
    print(f"[probe] daemon={BASE}")
    conn = json.loads(post_raw("/api/v1/acp/connect", {}))
    info = conn.get("data", conn)
    cid, token = info["connectionId"], info["sessionToken"]
    print(f"[probe] connectionId={cid}")

    acp_h = {
        "Content-Type": "application/json",
        "X-CodeBuddy-Request": "1",
        "acp-connection-id": cid,
        "acp-session-token": token,
        "Accept": "application/json, text/event-stream",
    }

    events = []
    stop = threading.Event()

    def listen():
        url = f"{BASE}/api/v1/acp?acp-connection-id={cid}"
        req = urllib.request.Request(url, headers={
            "X-CodeBuddy-Request": "1",
            "Accept": "text/event-stream",
        }, method="GET")
        try:
            with urllib.request.urlopen(req, timeout=90) as r:
                for raw in r:
                    line = raw.decode("utf-8", "replace")
                    if line.startswith("data:"):
                        p = line[5:].strip()
                        try:
                            o = json.loads(p)
                        except Exception:
                            continue
                        events.append(o)
                        m = o.get("method")
                        if m:
                            print(f"  << SSE method={m} id={o.get('id')}")
                        else:
                            print(f"  << SSE id={o.get('id')} result/error={'result' if 'result' in o else ('error' if 'error' in o else '?')}")
                    if stop.is_set():
                        break
        except Exception as e:
            print(f"[listen] ended: {e}")

    t = threading.Thread(target=listen, daemon=True)
    t.start()
    time.sleep(0.8)

    # initialize (response may be in POST body; also on SSE)
    b = post_raw("/api/v1/acp", {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {"protocolVersion": 1, "capabilities": {}, "clientInfo": {"name": "wb", "version": "0.1"}},
    }, acp_h)
    print(f"[init] POST body objs: {[o for o in parse_sse_blob(b) if 'result' in o or 'error' in o]}")
    time.sleep(0.6)

    b = post_raw("/api/v1/acp", {
        "jsonrpc": "2.0", "id": 2, "method": "session/new",
        "params": {"cwd": os.environ.get("WB_DAEMON_CWD", os.getcwd()), "mcpServers": []},
    }, acp_h)
    print(f"[session/new] POST body objs: {[o for o in parse_sse_blob(b) if o.get('id') == 2]}")
    time.sleep(0.6)

    # sessionId from events or POST body
    sid = None
    for o in parse_sse_blob(b):
        if o.get("id") == 2 and "result" in o:
            sid = o["result"].get("sessionId")
    if not sid:
        for o in events:
            if o.get("id") == 2 and "result" in o:
                sid = o["result"].get("sessionId")
    print(f"[session/new] sessionId={sid}")

    print("[session/prompt] POST (rely on SSE stream for deltas)")
    post_raw("/api/v1/acp", {
        "jsonrpc": "2.0", "id": 3, "method": "session/prompt",
        "params": {
            "sessionId": sid,
            "prompt": [{"type": "text", "text": "Reply with exactly the single word: PONG"}],
        },
    }, acp_h)

    print("[wait] up to 60s for SSE deltas...")
    for _ in range(120):
        time.sleep(0.5)
        if any(o.get("method") in ("session/notification",) and "completed" in str(o.get("params", {}).get("prompt", {}).get("status", "")) for o in events):
            time.sleep(1.0)
            break
        if any(o.get("method") == "session/prompt" and o.get("id") == 3 for o in events):
            # response received, maybe waiting for completion notification
            pass

    texts = []
    for o in events:
        m = o.get("method")
        params = o.get("params", {})
        msg = params.get("message") or params.get("messages")
        if isinstance(msg, dict) and msg.get("type") == "assistant":
            for blk in msg.get("content", []):
                if blk.get("type") == "text":
                    texts.append(blk["text"])
        if isinstance(msg, list):
            for mm in msg:
                if isinstance(mm, dict) and mm.get("type") == "assistant":
                    for blk in mm.get("content", []):
                        if blk.get("type") == "text":
                            texts.append(blk["text"])
    print(f"\n[result] assistant text: {''.join(texts)!r}")
    print(f"[result] total SSE events: {len(events)}")
    for o in events[:30]:
        print("   ev:", json.dumps(o, ensure_ascii=False)[:300])
    stop.set()
    time.sleep(0.3)
    print("[probe] done")


if __name__ == "__main__":
    main()
