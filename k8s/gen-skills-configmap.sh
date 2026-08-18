#!/usr/bin/env bash
# 把仓库 skills/ 目录生成为 ConfigMap（warm-start 共享技能），供 backend 挂载到 /app/skills。
# 用法（需 kubectl 且当前上下文指向目标集群）：
#   kubectl apply -f k8s/
#   bash k8s/gen-skills-configmap.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_SKILLS="${SCRIPT_DIR}/../skills"

if [ ! -d "$REPO_SKILLS" ]; then
  echo "未找到 skills 目录：$REPO_SKILLS" >&2
  exit 1
fi

kubectl create configmap go-multi-agent-skills \
  --from-file="$REPO_SKILLS" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "已创建/更新 ConfigMap go-multi-agent-skills（backend 挂载到 /app/skills，warm-start 即可命中种子技能）"
