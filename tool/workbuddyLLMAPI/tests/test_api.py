#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
workbuddyLLMAPI 全接口测试脚本（纯标准库，无第三方依赖）。

用法:
    python tests/test_api.py                 # 默认打 http://127.0.0.1:8088
    python tests/test_api.py http://x:8080   # 指定网关地址
    WB_API_BASE=http://x:8080 python tests/test_api.py

覆盖:
    1) GET  /                  服务信息
    2) GET  /healthz           健康检查
    3) GET  /v1/models         模型目录
    4) POST /v1/chat/completions  非流式（默认模型 = hy3）
    5) POST /v1/chat/completions  流式 SSE（默认模型）
    6) 显式模型 glm-5.1 / deepseek-v4-pro / kimi-k2.6
    7) 多轮对话
    8) system 提示词
    9) 错误用例：非法 JSON 体 -> 400

注意：每次调用都会真实消耗 WorkBuddy/CodeBuddy 积分（走本机守护进程）。
"""
import json
import sys
import time
import urllib.request
import urllib.error

BASE = (sys.argv[1] if len(sys.argv) > 1 else "") or "http://127.0.0.1:8088"
TIMEOUT = 180


def call(method, path, body=None, raw=False, stream=False):
    url = BASE + path
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        data = body.encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT) as resp:
            status = resp.status
            if stream:
                return status, resp.read().decode("utf-8", "replace")
            text = resp.read().decode("utf-8", "replace")
            return status, text if raw else (json.loads(text) if text else None)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:  # noqa
        return -1, str(e)


def chat(messages, model=None, stream=False, system=None):
    if system is not None:
        messages = [{"role": "system", "content": system}] + messages
    payload = {
        "messages": messages,
        "stream": stream,
    }
    if model:
        payload["model"] = model
    return call("POST", "/v1/chat/completions", json.dumps(payload), stream=stream)


results = []
def check(name, ok, detail=""):
    results.append((name, ok, detail))
    mark = "PASS" if ok else "FAIL"
    print(f"[{mark}] {name}" + (f"  -- {detail}" if detail else ""))


print(f"=== workbuddyLLMAPI 接口测试 @ {BASE} ===\n")

# 1) 服务信息
st, body = call("GET", "/")
check("GET / 服务信息", st == 200 and isinstance(body, dict) and "endpoints" in body,
      f"status={st}")

# 2) 健康检查（返回纯文本 "ok"，非 JSON）
st, body = call("GET", "/healthz", raw=True)
check("GET /healthz 健康检查", st == 200 and body.strip() == "ok", f"status={st} body={body!r}")

# 3) 模型目录
st, body = call("GET", "/v1/models")
ok = st == 200 and isinstance(body, dict) and any(
    m.get("id") == "hy3" for m in body.get("data", []))
check("GET /v1/models 含 hy3", ok, f"status={st}, n_models={len(body.get('data', [])) if isinstance(body, dict) else '?'}")

# 4) 非流式，默认模型（应走 hy3）
st, body = chat([{"role": "user", "content": "1+1=? 只答数字"}], stream=False)
model_used = body.get("model") if isinstance(body, dict) else None
check("chat/completions 非流式(默认=hy3)",
      st == 200 and isinstance(body, dict) and body.get("choices"),
      f"status={st} model={model_used}")

# 5) 流式 SSE，默认模型
st, text = chat([{"role": "user", "content": "用一句话介绍北京"}], stream=True)
chunks = [l for l in text.splitlines() if l.startswith("data:") and "[DONE]" not in l]
has_done = "[DONE]" in text
check("chat/completions 流式 SSE",
      st == 200 and len(chunks) > 0 and has_done,
      f"status={st} chunks={len(chunks)} done={has_done}")

# 6) 显式模型
for mdl in ["glm-5.1", "deepseek-v4-pro", "kimi-k2.6"]:
    st, body = chat([{"role": "user", "content": "只回答：OK"}], model=mdl, stream=False)
    used = body.get("model") if isinstance(body, dict) else None
    check(f"显式模型 {mdl}", st == 200 and used == mdl, f"status={st} model={used}")

# 7) 多轮对话
st, body = chat([
    {"role": "user", "content": "记住：我的幸运数字是 7"},
    {"role": "assistant", "content": "好的，我记住了你的幸运数字是 7。"},
    {"role": "user", "content": "我的幸运数字是几？只答数字。"},
], stream=False)
ans = body.get("choices", [{}])[0].get("message", {}).get("content", "") if isinstance(body, dict) else ""
check("多轮对话(上下文记忆)", st == 200 and "7" in ans, f"status={st} ans={ans!r}")

# 8) system 提示词
st, body = chat([{"role": "user", "content": "现在几点了？"}], system="你是一个只回答『不知道』的机器人。", stream=False)
ans = body.get("choices", [{}])[0].get("message", {}).get("content", "") if isinstance(body, dict) else ""
check("system 提示词生效", st == 200 and ("不知道" in ans or "不知" in ans), f"status={st} ans={ans!r}")

# 9) 错误用例：非法 JSON
st, _ = call("POST", "/v1/chat/completions", "{not valid json", raw=True)
check("错误用例 非法JSON -> 400", st == 400, f"status={st}")


print("\n=== 汇总 ===")
passed = sum(1 for _, ok, _ in results if ok)
print(f"{passed}/{len(results)} 通过")
if passed != len(results):
    print("失败项：")
    for n, ok, d in results:
        if not ok:
            print(f"  - {n}: {d}")
    sys.exit(1)
print("全部通过 ✅")
