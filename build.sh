#!/usr/bin/env bash
set -euo pipefail

# build.sh — 讀 secrets.env，用 ldflags 將機密注入並編譯 dropbox-uploader。
# 預設為 dry-run（僅顯示動作）；加 --go 才真正編譯。

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SECRETS_FILE="${SCRIPT_DIR}/secrets.env"
PKG="github.com/gilbertchiao/dropbox-uploader/internal/config"
# 編譯產物統一輸出到 bin/ 子目錄，避免散落在專案根目錄。
BUILD_DIR="${SCRIPT_DIR}/bin"
OUTPUT="${BUILD_DIR}/dropbox-uploader"

# 顯示腳本用法說明。
usage() {
  cat <<EOF
用法：build.sh [選項]

讀取 secrets.env 中的機密，透過 go -ldflags 注入並編譯 dropbox-uploader。
採 --go 風格：預設為 dry-run（僅顯示將執行的動作），加 --go 才真正編譯。

選項：
  --go          真正執行編譯（不加此旗標時僅為 dry-run）
  -h, --help    顯示本說明並結束

輸出：
  編譯產物會輸出至 ${BUILD_DIR}/dropbox-uploader

範例：
  ./build.sh          # dry-run，只顯示將執行的指令
  ./build.sh --go     # 真正編譯，產出 bin/dropbox-uploader
EOF
}

# 解析參數：逐一檢查所有參數，任何未知參數都報錯退出。
# --help/-h 出現在任何位置都優先顯示說明後結束。
DRY_RUN=true
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h | --help)
      usage
      exit 0
      ;;
    --go)
      DRY_RUN=false
      ;;
    *)
      echo "錯誤：未知參數 '${1}'" >&2
      echo >&2
      usage >&2
      exit 1
      ;;
  esac
  shift
done

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

# 版本字串取自 git：有 tag 時為 tag（如 v1.0.0，工作區有未提交變更時加 -dirty）；
# 無 tag 但在 git repo 中時退回短 commit hash（--always）；完全不在 git repo 時退回 dev。
VERSION="$(git -C "$SCRIPT_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)"
reject_single_quote VERSION "$VERSION"

LDFLAGS="-s -w"
LDFLAGS+=" -X '${PKG}.AppKey=${DROPBOX_APP_KEY}'"
LDFLAGS+=" -X '${PKG}.AppSecret=${DROPBOX_APP_SECRET}'"
LDFLAGS+=" -X '${PKG}.RefreshToken=${DROPBOX_REFRESH_TOKEN}'"
LDFLAGS+=" -X '${PKG}.TargetDir=${DROPBOX_TARGET_DIR}'"
LDFLAGS+=" -X 'main.version=${VERSION}'"

if $DRY_RUN; then
  echo "[dry-run] 將建立輸出目錄：${BUILD_DIR}"
  echo "[dry-run] 將執行：CGO_ENABLED=0 go build -trimpath -ldflags \"<已隱藏機密>\" -o ${OUTPUT} ."
  echo "[dry-run] 加上 --go 參數才會真正編譯。"
  exit 0
fi

# 確保輸出目錄存在（bin/ 已列入 .gitignore，不會進版控）。
mkdir -p "$BUILD_DIR"

echo "編譯中…"
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$OUTPUT" .
echo "完成：${OUTPUT}"
