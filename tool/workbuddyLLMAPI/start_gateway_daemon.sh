#!/usr/bin/env bash
#
# 本地一键启动脚本（个人使用；已去除任何硬编码用户名/绝对私密路径，可安全提交）。
#
# 作用：
#   1) 启动本机 CodeBuddy/WorkBuddy 守护进程（ACP 桥接层，复用已登录态消耗本机积分）
#   2) 编译并启动 workbuddyLLMAPI 网关（OpenAI 兼容，默认 codebuddy 后端）
#
# 所有本机相关路径均通过环境变量覆盖，默认值基于 $LOCALAPPDATA 自动适配当前用户，
# 不再写死任何用户名。运行前如需覆盖，export 对应变量即可，例如：
#   export WB_CLI_PATH="$LOCALAPPDATA/Programs/WorkBuddy/resources/app.asar.unpacked/cli/bin/codebuddy"
#   export WB_DAEMON_CWD="$HOME/Desktop/Claude-Code-MultiAgent2"
#
# 用法：
#   ./start_gateway_daemon.sh           # 启动 守护进程 + 网关（默认）
#   ./start_gateway_daemon.sh probe      # 仅运行 ACP 桥接探测（bridge/acp_probe.py）验证守护进程连通性
set -euo pipefail

# ---- 可覆盖的环境变量（默认值已尽量机器无关，不含私密绝对路径）----
# WorkBuddy/CodeBuddy CLI 可执行文件路径（含本机用户名，故默认用 $LOCALAPPDATA 推导）
WB_CLI_PATH="${WB_CLI_PATH:-$LOCALAPPDATA/Programs/WorkBuddy/resources/app.asar.unpacked/cli/bin/codebuddy}"
# 守护进程监听地址（ACP 桥接）
WB_DAEMON_HOST="${WB_DAEMON_HOST:-127.0.0.1}"
WB_DAEMON_PORT="${WB_DAEMON_PORT:-18765}"
WB_DAEMON_URL="${WB_DAEMON_URL:-http://$WB_DAEMON_HOST:$WB_DAEMON_PORT}"
# 网关监听地址
WB_LISTEN="${WB_LISTEN:-:8088}"
# 网关后端：codebuddy | passthrough | mock
WB_BACKEND="${WB_BACKEND:-codebuddy}"
# 默认模型 / 回退模型（仅 codebuddy 后端、且请求未显式指定模型时生效）
WB_DAEMON_MODEL="${WB_DAEMON_MODEL:-hy3}"
WB_DAEMON_FALLBACK_MODEL="${WB_DAEMON_FALLBACK_MODEL:-deepseek-v4-pro}"
# ACP 工作目录（守护进程 agent 的 cwd；按需修改，默认当前目录）
WB_DAEMON_CWD="${WB_DAEMON_CWD:-$(pwd)}"
# Python 解释器（运行 bridge/acp_probe.py 用）
PY="${PYTHON:-python3}"

# 脚本所在目录（网关源码目录）
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cmd="${1:-start}"

if [ "$cmd" = "probe" ]; then
  echo "[probe] 运行 ACP 桥接探测 -> $WB_DAEMON_URL"
  if [ ! -x "$WB_CLI_PATH" ]; then
    echo "[warn] 未找到 WorkBuddy CLI: $WB_CLI_PATH（如需覆盖请 export WB_CLI_PATH）"
    exit 1
  fi
  cd "$SCRIPT_DIR"
  WB_DAEMON_CWD="$WB_DAEMON_CWD" "$PY" bridge/acp_probe.py "$WB_DAEMON_URL"
  exit 0
fi

if [ "$cmd" != "start" ]; then
  echo "usage: $0 [start|probe]" >&2
  exit 1
fi

echo "[start] 1/2 启动 CodeBuddy 守护进程 (daemon) @ $WB_DAEMON_URL"
if [ ! -x "$WB_CLI_PATH" ]; then
  echo "[error] 未找到 WorkBuddy CLI: $WB_CLI_PATH" >&2
  echo "        请设置环境变量 WB_CLI_PATH 指向本机 codebuddy CLI 后重试。" >&2
  exit 1
fi
# 若守护进程端口已开放则视为已在运行（幂等保护，避免重复拉起冲突）
if ! python3 -c "import socket,sys; s=socket.socket(); s.settimeout(1); s.connect((sys.argv[1],int(sys.argv[2]))); s.close()" "$WB_DAEMON_HOST" "$WB_DAEMON_PORT" 2>/dev/null; then
  # 注意：daemon start 不会自行返回（它是常驻/监管进程），必须以 & 后台启动，
  # 否则会卡住后续“编译并启动网关”步骤。日志写入 daemon.log 便于排查。
  nohup "$WB_CLI_PATH" daemon start --port "$WB_DAEMON_PORT" --host "$WB_DAEMON_HOST" >"$SCRIPT_DIR/daemon.log" 2>&1 &
  echo "[start] daemon 启动中（后台），等待端口就绪..."
  python3 -c "import socket,sys,time; h,p=sys.argv[1],int(sys.argv[2])
for _ in range(60):
    s=socket.socket(); s.settimeout(1)
    try:
        s.connect((h,p)); print('detect: daemon ready'); break
    except Exception:
        time.sleep(0.5)
    finally:
        s.close()" "$WB_DAEMON_HOST" "$WB_DAEMON_PORT"
else
  echo "[start] 守护进程已在运行，跳过启动。"
fi

echo "[start] 2/2 编译并启动 workbuddyLLMAPI 网关 (backend=$WB_BACKEND listen=$WB_LISTEN)"
cd "$SCRIPT_DIR"
go build -o bin/workbuddy-llm-api.exe .
# 用 exec 让网关进程接管 shell，便于正确接收 Ctrl-C / 终止信号
exec env \
  WB_LISTEN="$WB_LISTEN" \
  WB_BACKEND="$WB_BACKEND" \
  WB_DAEMON_URL="$WB_DAEMON_URL" \
  WB_DAEMON_CWD="$WB_DAEMON_CWD" \
  WB_DAEMON_MODEL="$WB_DAEMON_MODEL" \
  WB_DAEMON_FALLBACK_MODEL="$WB_DAEMON_FALLBACK_MODEL" \
  ./bin/workbuddy-llm-api.exe
