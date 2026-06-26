#!/usr/bin/env bash
set -euo pipefail

# build.sh — 讀 secrets.env，用 ldflags 將機密注入並編譯 dropbox-uploader。
# 預設為 dry-run（僅顯示動作）；加 --go 才真正編譯。

DRY_RUN=true
[[ "${1:-}" == "--go" ]] && DRY_RUN=false

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SECRETS_FILE="${SCRIPT_DIR}/secrets.env"
PKG="github.com/gilbertchiao/dropbox-uploader/internal/config"
OUTPUT="${SCRIPT_DIR}/dropbox-uploader"

if [[ ! -f "$SECRETS_FILE" ]]; then
  echo "錯誤：找不到 ${SECRETS_FILE}" >&2
  echo "請複製 secrets.env.example 為 secrets.env 並填入實際值。" >&2
  exit 1
fi

# shellcheck disable=SC1090
source "$SECRETS_FILE"

: "${DROPBOX_APP_KEY:?secrets.env 缺少 DROPBOX_APP_KEY}"
: "${DROPBOX_APP_SECRET:?secrets.env 缺少 DROPBOX_APP_SECRET}"
: "${DROPBOX_REFRESH_TOKEN:?secrets.env 缺少 DROPBOX_REFRESH_TOKEN}"
: "${DROPBOX_TARGET_DIR:?secrets.env 缺少 DROPBOX_TARGET_DIR}"

# go -ldflags 以單引號包覆每個 -X 值，才能支援含空白的路徑；
# 值本身不可含單引號（否則會破壞引用），先行拒絕。
reject_single_quote() {
  local name="$1" value="$2"
  if [[ "$value" == *"'"* ]]; then
    echo "錯誤：${name} 不可包含單引號 (')" >&2
    exit 1
  fi
}
reject_single_quote DROPBOX_APP_KEY "$DROPBOX_APP_KEY"
reject_single_quote DROPBOX_APP_SECRET "$DROPBOX_APP_SECRET"
reject_single_quote DROPBOX_REFRESH_TOKEN "$DROPBOX_REFRESH_TOKEN"
reject_single_quote DROPBOX_TARGET_DIR "$DROPBOX_TARGET_DIR"

# 版本取自 git tag（如 v1.0.0）；無 git 或無 tag 時退回 dev。
VERSION="$(git -C "$SCRIPT_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)"
reject_single_quote VERSION "$VERSION"

LDFLAGS="-s -w"
LDFLAGS+=" -X '${PKG}.AppKey=${DROPBOX_APP_KEY}'"
LDFLAGS+=" -X '${PKG}.AppSecret=${DROPBOX_APP_SECRET}'"
LDFLAGS+=" -X '${PKG}.RefreshToken=${DROPBOX_REFRESH_TOKEN}'"
LDFLAGS+=" -X '${PKG}.TargetDir=${DROPBOX_TARGET_DIR}'"
LDFLAGS+=" -X 'main.version=${VERSION}'"

if $DRY_RUN; then
  echo "[dry-run] 將執行：CGO_ENABLED=0 go build -trimpath -ldflags \"<已隱藏機密>\" -o ${OUTPUT} ."
  echo "[dry-run] 加上 --go 參數才會真正編譯。"
  exit 0
fi

echo "編譯中…"
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$OUTPUT" .
echo "完成：${OUTPUT}"
