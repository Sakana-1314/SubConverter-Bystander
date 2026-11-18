#!/usr/bin/env bash
# 镜像冒烟：1) 旁路注入流量头；2) 内置转换器可用。
set -euo pipefail

IMAGE="${IMAGE:-local/bystander:verify}"
NAME=bystander-smoke
PORT=18080
PROVIDER_PORT=19090
headers="$(mktemp)"
body="$(mktemp)"
FAKE_PID=""

cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  if [ -n "$FAKE_PID" ]; then kill "$FAKE_PID" >/dev/null 2>&1 || true; fi
  rm -f "$headers" "$body"
}
trap cleanup EXIT

python3 tests/fake_upstream.py "$PROVIDER_PORT" &
FAKE_PID=$!
for _ in $(seq 1 30); do
  curl -fsS "http://127.0.0.1:${PROVIDER_PORT}/provider" -o /dev/null 2>/dev/null && break
  sleep 0.2
done

# 1) 假转换器 + 假订阅：验证转换结果和订阅源的流量头一起返回。
docker run -d --name "$NAME" --network host \
  -e LISTEN=":${PORT}" \
  -e CONVERTER_CMD= \
  -e CONVERTER_URL="http://127.0.0.1:${PROVIDER_PORT}" \
  -e ALLOW_PRIVATE_TARGETS=1 \
  "$IMAGE" >/dev/null
for _ in $(seq 1 30); do
  curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 && break
  sleep 0.5
done

curl -fsS -D "$headers" -o "$body" \
  "http://127.0.0.1:${PORT}/sub?target=clash&url=http%3A%2F%2F127.0.0.1%3A${PROVIDER_PORT}%2Fprovider"
grep -qi '^Subscription-Userinfo: upload=1048576' "$headers"
grep -qi '^profile-update-interval: 24' "$headers"
grep -q 'proxy-providers:' "$body"
docker rm -f "$NAME" >/dev/null

# 2) 默认镜像：内置转换器应随容器启动并可经旁路访问。
docker run -d --name "$NAME" -p "${PORT}:8080" "$IMAGE" >/dev/null
for _ in $(seq 1 60); do
  curl -fsS "http://127.0.0.1:${PORT}/version" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "http://127.0.0.1:${PORT}/version" >/dev/null

echo "冒烟测试通过"
