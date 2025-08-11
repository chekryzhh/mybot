#!/usr/bin/env bash
set -euo pipefail

REMOTE_HOST="poster"
APP_NAME="${APP_NAME:-support_poster}"
REMOTE_DIR="/opt/${APP_NAME}"
BIN_LOCAL_DIR="./build"
BIN_NAME="${APP_NAME}"
BIN_LOCAL_PATH="${BIN_LOCAL_DIR}/${BIN_NAME}"
GO_MAIN="${GO_MAIN:-.}"   # если main.go в cmd/server -> ./cmd/server

RUN_TESTS="${RUN_TESTS:-1}"
SYNC_STATIC="${SYNC_STATIC:-0}"
SYNC_MIGRATIONS="${SYNC_MIGRATIONS:-0}"
BRANCH="${BRANCH:-$(git rev-parse --abbrev-ref HEAD)}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "❌ Требуется $1"; exit 1; }; }
need git; need ssh; need go
USE_RSYNC=1; command -v rsync >/dev/null 2>&1 || USE_RSYNC=0

echo "→ Комментарий к коммиту:"
read -r COMMIT_MSG
[ -z "${COMMIT_MSG}" ] && { echo "❌ Пустой комментарий"; exit 1; }

git add -A
if ! git diff --cached --quiet; then git commit -m "${COMMIT_MSG}"; fi
git push origin "${BRANCH}"

ARCH_RAW="$(ssh "${REMOTE_HOST}" 'uname -m' || echo x86_64)"
case "$ARCH_RAW" in x86_64) GOARCH=amd64 ;; aarch64|arm64) GOARCH=arm64 ;; *) GOARCH=amd64 ;; esac
GOOS=linux
mkdir -p "${BIN_LOCAL_DIR}"
[ "${RUN_TESTS}" = "1" ] && go test ./...
GOOS="${GOOS}" GOARCH="${GOARCH}" go build -o "${BIN_LOCAL_PATH}" "${GO_MAIN}" && strip "${BIN_LOCAL_PATH}" >/dev/null 2>&1 || true

ssh "${REMOTE_HOST}" "mkdir -p '${REMOTE_DIR}'"
if [ "${USE_RSYNC}" = "1" ]; then
  rsync -avz --progress "${BIN_LOCAL_PATH}" "${REMOTE_HOST}:${REMOTE_DIR}/${BIN_NAME}"
else
  scp "${BIN_LOCAL_PATH}" "${REMOTE_HOST}:${REMOTE_DIR}/${BIN_NAME}"
fi

ssh "${REMOTE_HOST}" "systemctl daemon-reload && systemctl restart '${APP_NAME}' && sleep 1 && systemctl --no-pager --full status '${APP_NAME}' | tail -n 40"
echo "✅ Готово"
