#!/usr/bin/env python3
"""CodeBuddy Agent SDK 桥接 sidecar。

由 Go 网关（backend=codebuddy）以子进程方式调用：
  - 从 stdin 读取一行 JSON 请求：{"system": str, "messages": [{"role","content"}], "model": str, "stream": bool}
  - 通过 codebuddy-agent-sdk 的 query() 执行（消耗 CodeBuddy/WorkBuddy 账号积分）
  - 向 stdout 输出 NDJSON 事件流：
      {"type":"delta","text": "..."}      # 文本增量
      {"type":"done","usage": {...}}      # 结束（含 usage）
      {"type":"error","message": "..."}   # 错误

说明：CodeBuddy Agent SDK 是一个「Agent」而非裸 LLM 端点，因此这里关闭工具
（allowed_tools=[]）并将其当作一次性的对话补全来用。模型名会透传给 SDK。
"""

import json
import os
import sys
import asyncio

try:
    from codebuddy_agent_sdk import (
        query,
        AssistantMessage,
        TextBlock,
        ResultMessage,
        CodeBuddyAgentOptions,
    )
except ImportError as e:  # pragma: no cover
    sys.stdout.write(json.dumps({"type": "error", "message": "codebuddy_agent_sdk 未安装: %s" % e}) + "\n")
    sys.stdout.flush()
    sys.exit(1)


def emit(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def build_prompt(system, messages):
    parts = []
    if system:
        parts.append("System: " + system)
    for m in messages:
        role = m.get("role", "user")
        content = m.get("content", "")
        parts.append("%s: %s" % (role, content))
    return "\n".join(parts)


async def run(payload):
    system = payload.get("system", "")
    messages = payload.get("messages", [])
    model = payload.get("model", None)

    prompt = build_prompt(system, messages)

    env = {}
    api_key = os.environ.get("CODEBUDDY_API_KEY")
    if api_key:
        env["CODEBUDDY_API_KEY"] = api_key
    cb_env = os.environ.get("CODEBUDDY_INTERNET_ENVIRONMENT")
    if cb_env:
        env["CODEBUDDY_INTERNET_ENVIRONMENT"] = cb_env

    options = CodeBuddyAgentOptions(
        permission_mode="bypassPermissions",
        allowed_tools=[],
        model=model,
        env=env,
    )

    try:
        async for message in query(prompt=prompt, options=options):
            if isinstance(message, AssistantMessage):
                for block in message.content:
                    if isinstance(block, TextBlock):
                        emit({"type": "delta", "text": block.text})
            elif isinstance(message, ResultMessage):
                emit({"type": "done", "usage": message.usage or {}})
    except Exception as e:  # pragma: no cover
        emit({"type": "error", "message": str(e)})


def main():
    raw = sys.stdin.readline()
    if not raw.strip():
        emit({"type": "error", "message": "empty request"})
        return
    try:
        payload = json.loads(raw)
    except Exception as e:
        emit({"type": "error", "message": "bad json: %s" % e})
        return
    asyncio.run(run(payload))


if __name__ == "__main__":
    main()
