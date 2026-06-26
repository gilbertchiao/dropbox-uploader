# dropbox-uploader 設計文件

- 日期：2026-06-26
- 狀態：已實作完成（PR #1–#4 已合併至 main）

## 1. 目標與用途

提供一個**單一靜態執行檔**，部署到 server 上，把指定檔案（主要是 log 檔）上傳備份到固定的 Dropbox 資料夾。

使用方式：

```bash
dropbox-uploader <檔案路徑>
# 例：dropbox-uploader /var/log/myapp/app.log
```

核心需求：

- server 上只放一個編譯好的 binary，**不需要設定檔**。
- app key / secret / refresh token / 目標資料夾路徑等機密，在 **build 時用 `-ldflags -X` 注入**進 binary；原始碼與 git 倉庫不含任何機密。
- 支援**任意大小**檔案（含 >150MB）。

## 2. 安全模型與取捨（重要）

- **編入 binary ≠ 真正隱藏**：任何能讀到 binary 的人，都可用 `strings` 等工具撈出注入的機密。`-ldflags` 注入的價值在於「server 上沒有明文設定檔、不一眼可見」，而非加密。
- 真正的防護重點是**限縮外洩後的傷害範圍**，因此 Dropbox App 權限類型採用 **App folder（應用程式資料夾）**：此 app 只能存取專屬的 `Apps/<App名>/` 目錄，碰不到使用者 Dropbox 其他檔案。即使 token 外洩，影響僅限於該備份資料夾。
- 機密在開發機以**未進版控的 `secrets.env`** 保存，僅在 build 時讀取注入，不進 git、不進 server。

## 3. 技術選型

- **語言**：Go（Go Modules，module 名 `github.com/gilbertchiao/dropbox-uploader`）。
- **HTTP**：標準庫 `net/http` 直接呼叫 Dropbox HTTP API，**不引入 Dropbox SDK**。
  - 優點：零外部相依、編出單一靜態 binary、最穩、符合「丟一個檔上 server」的需求。
  - Dropbox 官方 Go SDK 為 unofficial 維護且體積偏大，對此小工具屬殺雞用牛刀。
- **日誌**：標準庫 `log/slog`（結構化）。
- **測試**：標準 `testing` + `httptest` 模擬 Dropbox API；斷言用 `testify`。

## 4. 專案結構

```
dropbox-uploader/
├── go.mod
├── main.go                      # CLI 進入點、參數解析、流程控制、exit code
├── internal/
│   ├── config/
│   │   ├── config.go            # ldflags 注入變數 + 驗證
│   │   └── config_test.go
│   └── dropbox/
│       ├── client.go            # token 換新 + simple/session 上傳
│       └── client_test.go
├── build.sh                     # 讀 secrets.env，用 ldflags 注入並編譯（--go 風格）
├── secrets.env.example          # 機密範本（實際 secrets.env 不進版控）
├── .gitignore                   # 排除 secrets.env、編出的 binary、work/temp/tmp
├── docs/
└── README.md
```

## 5. 設定注入（ldflags）

build 時注入到 `internal/config` 套件的下列變數：

| 變數            | 說明                                   |
| --------------- | -------------------------------------- |
| `AppKey`        | Dropbox App key                        |
| `AppSecret`     | Dropbox App secret                     |
| `RefreshToken`  | 長效 refresh token                     |
| `TargetDir`     | 目標資料夾，如 `/logs`（App folder 下相對根目錄） |

`config.Validate()` 在啟動時檢查四個值皆非空，否則回報設定錯誤並以對應 exit code 結束。

## 6. 認證與上傳流程

1. **啟動**：載入注入設定 → `Validate()`；檢查目標檔案存在、可讀、取得大小。
2. **換 access token**：`POST https://api.dropbox.com/oauth2/token`
   - body：`grant_type=refresh_token&refresh_token=<RefreshToken>`
   - app key/secret 以 HTTP Basic Auth 帶入。
   - 取得短效 `access_token`。
3. **上傳**：目標路徑 = `TargetDir + "/" + filepath.Base(檔名)`，上傳模式固定 `overwrite`（同名覆寫）。
   - **檔案 ≤ 150MB**：`POST https://content.dropboxapi.com/2/files/upload`
     - header `Dropbox-API-Arg` 帶 JSON：`{"path": "...", "mode": "overwrite", "mute": true}`
     - body 為檔案二進位內容。
   - **檔案 > 150MB**：分段 upload session
     - `files/upload_session/start`（第一段，取得 `session_id`）
     - `files/upload_session/append_v2`（中間各段，帶 offset）
     - `files/upload_session/finish`（最後一段 + commit：path / mode=overwrite）
     - 每段大小 **8MB**（4MB 倍數，符合 Dropbox 建議）。

## 7. 錯誤處理與 exit code

- 所有 error 向上包裝用 `fmt.Errorf("...: %w", err)` 保留錯誤鏈；以 `log/slog` 輸出結構化日誌（含檔名、大小、目標路徑、耗時等欄位）。
- exit code 規劃（方便 server 上 cron/腳本判斷）：

| code | 意義                         |
| ---- | ---------------------------- |
| 0    | 成功                         |
| 1    | 參數錯誤（缺檔名 / 用法錯）  |
| 2    | 檔案問題（不存在 / 不可讀） |
| 3    | 設定錯誤（注入機密缺失）    |
| 4    | 認證失敗（token 換取失敗）  |
| 5    | 上傳失敗（API 回錯 / 網路） |

## 8. build.sh 行為

- 採 `--go` 風格：預設僅顯示將執行的動作（dry-run），加 `--go` 才真正編譯。
- 從本機 `secrets.env` 讀取四個機密值，組出 `-ldflags "-X github.com/gilbertchiao/dropbox-uploader/internal/config.AppKey=... -X ..."`（需用完整 import path），執行 `go build` 產出 `dropbox-uploader`。
- 若 `secrets.env` 不存在則提示複製 `secrets.env.example` 並填值。

## 9. 測試範圍

- `config_test.go`：驗證 `Validate()` 對缺值的判斷、目標路徑組合。
- `client_test.go`：以 `httptest` 模擬 Dropbox endpoint，覆蓋
  - token 換取成功 / 失敗；
  - simple upload（小檔）成功 / API 回錯；
  - session upload（大檔）start/append/finish 流程與 offset 正確性。

## 10. 明確排除（YAGNI）

- 不做 OAuth 瀏覽器授權流程（refresh token 於 build 時注入）。
- 不做設定檔 / 環境變數讀取（機密一律編入）。
- 不做同名加時間戳保留歷史（固定覆寫）。
- 不做多檔 / 萬用字元批次上傳（一次一檔）。
