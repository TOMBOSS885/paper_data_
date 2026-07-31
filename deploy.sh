#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="$ROOT_DIR/.env"
COMPOSE_FILE="$ROOT_DIR/deploy/docker-compose.yml"

if ! command -v docker >/dev/null 2>&1; then
  echo "错误：未安装 Docker Engine。请先安装 Docker 24+ 和 Compose v2。" >&2
  exit 1
fi

if ! docker compose version >/dev/null 2>&1; then
  echo "错误：未安装 Docker Compose v2。" >&2
  exit 1
fi

if [[ ! -f "$ENV_FILE" ]]; then
  cp "$ROOT_DIR/.env.example" "$ENV_FILE"
  echo "已创建 .env。请填写 MYSQL_PASSWORD、JWT_SECRET、SETUP_SECRET 后重新执行 ./deploy.sh。" >&2
  exit 1
fi

if grep -Eq 'replace_with_' "$ENV_FILE"; then
  echo "错误：.env 仍包含示例值。请先填写 MYSQL_PASSWORD、JWT_SECRET 和 SETUP_SECRET。" >&2
  exit 1
fi

docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" config --quiet
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --build --remove-orphans

HTTP_PORT="$(grep -E '^HTTP_PORT=' "$ENV_FILE" | tail -n 1 | cut -d= -f2- || true)"
HTTP_PORT="${HTTP_PORT:-8081}"
PUBLIC_BASE_URL="$(grep -E '^PUBLIC_BASE_URL=' "$ENV_FILE" | tail -n 1 | cut -d= -f2- || true)"
PUBLIC_BASE_URL="${PUBLIC_BASE_URL:-https://papers.example.com}"
echo "等待服务启动……"
for _ in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:${HTTP_PORT}/healthz" >/dev/null 2>&1; then
    echo "部署完成：${PUBLIC_BASE_URL%/}/setup"
    echo "首次初始化时输入 .env 中的 SETUP_SECRET 创建管理员账号。"
    exit 0
  fi
  sleep 3
done

echo "服务未在预期时间内就绪，显示最近日志：" >&2
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" logs --tail=100 api web >&2
exit 1
