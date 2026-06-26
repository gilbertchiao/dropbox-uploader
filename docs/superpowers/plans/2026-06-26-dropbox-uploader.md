# dropbox-uploader Implementation Plan

> **狀態：已實作完成。** 四個階段（Task 1–4）皆已透過 PR #1–#4 實作並合併至 main，
> 內含 GitHub Copilot review 的修正。本文件保留為實作當時的計畫紀錄，後續以原始碼與
> spec 為準。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 一個單一靜態 Go binary，用法 `dropbox-uploader <檔案>`，把檔案（主要是 log）上傳備份到固定的 Dropbox App folder，機密於 build 時以 ldflags 注入。

**Architecture:** 標準庫 `net/http` 直接呼叫 Dropbox HTTP API。`internal/config` 持有 ldflags 注入值與驗證；`internal/dropbox` 負責 token 換取與上傳（≤150MB simple、>150MB session 分段）；`main.go` 串接並對應 exit code。

**Tech Stack:** Go 1.22、標準庫（net/http、log/slog、encoding/json）、測試用 testing + httptest + testify（僅測試相依，不進 production binary）。

## Global Constraints

- module 名：`github.com/gilbertchiao/dropbox-uploader`
- production binary 零外部執行期相依（testify 僅測試用）
- 機密（AppKey/AppSecret/RefreshToken/TargetDir）一律由 `-ldflags -X` 注入，原始碼與 git 不含實際值
- 上傳模式固定 `overwrite`（同名覆寫）
- simple upload 上限 150MB（`150 * 1024 * 1024`）；超過走 session，分段 8MB（`8 * 1024 * 1024`）
- Dropbox App 權限類型：App folder
- 日誌用 `log/slog`；error 向上包裝用 `fmt.Errorf("...: %w", err)`
- exit code：0 成功 / 1 參數錯 / 2 檔案問題 / 3 設定錯 / 4 認證失敗 / 5 上傳失敗

## File Structure

- `go.mod` — module 定義
- `internal/config/config.go` — ldflags 注入變數、`Config`、`Load`、`Validate`、`RemotePath`
- `internal/config/config_test.go` — config 單元測試
- `internal/dropbox/client.go` — `Client`、token 換取、simple/session 上傳、`AuthError`
- `internal/dropbox/client_test.go` — 以 httptest 模擬 Dropbox API 的測試
- `main.go` — CLI 進入點、參數解析、流程、exit code
- `build.sh` — 讀 secrets.env、ldflags 注入並編譯（--go 風格）
- `secrets.env.example` — 機密範本
- `.gitignore` — 排除 secrets.env、binary、work/temp/tmp
- `README.md` — 使用與建置說明

## 階段與 PR 對應

- **Stage 1（PR #1）**：專案骨架 + config 套件（go.mod、.gitignore、config.go、測試）
- **Stage 2（PR #2）**：dropbox client — token 換取 + simple upload
- **Stage 3（PR #3）**：dropbox client — session 分段上傳（大檔）
- **Stage 4（PR #4）**：main.go 整合 + build.sh + secrets.env.example + README

---

### Task 1: 專案骨架 + config 套件

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - 套件變數 `config.AppKey, config.AppSecret, config.RefreshToken, config.TargetDir`（string，ldflags 注入）
  - `type Config struct { AppKey, AppSecret, RefreshToken, TargetDir string }`
  - `func Load() (*Config, error)`
  - `func (c *Config) Validate() error`
  - `func (c *Config) RemotePath(filename string) (string, error)`

- [ ] **Step 1: 建立 go.mod**

```
module github.com/gilbertchiao/dropbox-uploader

go 1.22
```

執行：`go mod edit -require=github.com/stretchr/testify@v1.9.0`（測試相依，稍後 `go mod tidy` 補齊）

- [ ] **Step 2: 建立 .gitignore**

```
# 機密
secrets.env

# 編譯產物
/dropbox-uploader

# Working/Temporary directories
work/
temp/
tmp/
```

- [ ] **Step 3: 寫 config 測試（先失敗）**

`internal/config/config_test.go`：

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidate_AllPresent(t *testing.T) {
	c := &Config{AppKey: "k", AppSecret: "s", RefreshToken: "r", TargetDir: "/logs"}
	require.NoError(t, c.Validate())
}

func TestValidate_MissingFields(t *testing.T) {
	c := &Config{AppKey: "k"} // 缺 secret/refresh/dir
	err := c.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "AppSecret")
	require.Contains(t, err.Error(), "RefreshToken")
	require.Contains(t, err.Error(), "TargetDir")
}

func TestRemotePath_Basic(t *testing.T) {
	c := &Config{TargetDir: "/logs"}
	got, err := c.RemotePath("/var/log/app.log")
	require.NoError(t, err)
	require.Equal(t, "/logs/app.log", got)
}

func TestRemotePath_TrailingSlash(t *testing.T) {
	c := &Config{TargetDir: "logs/"}
	got, err := c.RemotePath("app.log")
	require.NoError(t, err)
	require.Equal(t, "/logs/app.log", got)
}
```

- [ ] **Step 4: 執行測試確認失敗**

Run: `go test ./internal/config/...`
Expected: 編譯失敗（Config / Validate / RemotePath 尚未定義）

- [ ] **Step 5: 實作 config.go**

`internal/config/config.go`：

```go
// Package config 提供 build 時透過 ldflags 注入的設定值與其驗證。
package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// 下列變數由 build 時的 -ldflags "-X" 注入，原始碼不含實際機密值。
var (
	AppKey       string // Dropbox App key
	AppSecret    string // Dropbox App secret
	RefreshToken string // 長效 refresh token
	TargetDir    string // 目標資料夾，例如 "/logs"
)

// Config 為驗證後的設定。
type Config struct {
	AppKey       string
	AppSecret    string
	RefreshToken string
	TargetDir    string
}

// Load 回傳由 ldflags 注入的設定並驗證其完整性。
func Load() (*Config, error) {
	c := &Config{
		AppKey:       strings.TrimSpace(AppKey),
		AppSecret:    strings.TrimSpace(AppSecret),
		RefreshToken: strings.TrimSpace(RefreshToken),
		TargetDir:    strings.TrimSpace(TargetDir),
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate 檢查所有必要欄位皆已設定。
func (c *Config) Validate() error {
	var missing []string
	if c.AppKey == "" {
		missing = append(missing, "AppKey")
	}
	if c.AppSecret == "" {
		missing = append(missing, "AppSecret")
	}
	if c.RefreshToken == "" {
		missing = append(missing, "RefreshToken")
	}
	if c.TargetDir == "" {
		missing = append(missing, "TargetDir")
	}
	if len(missing) > 0 {
		return fmt.Errorf("設定不完整，缺少注入值: %s", strings.Join(missing, ", "))
	}
	return nil
}

// RemotePath 依目標資料夾與來源檔名（取 basename）組出 Dropbox 上傳路徑。
func (c *Config) RemotePath(filename string) (string, error) {
	base := filepath.Base(filename)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "", errors.New("無法從檔名取得有效的 basename")
	}
	dir := "/" + strings.Trim(c.TargetDir, "/")
	return dir + "/" + base, nil
}
```

- [ ] **Step 6: go mod tidy 並執行測試**

Run: `go mod tidy && go test ./internal/config/...`
Expected: PASS（4 個測試）

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum .gitignore internal/config/
git commit -m "feat(config): add ldflags-injected config with validation and remote path"
```

---

### Task 2: dropbox client — token 換取 + simple upload

**Files:**
- Create: `internal/dropbox/client.go`
- Test: `internal/dropbox/client_test.go`

**Interfaces:**
- Consumes: 無（獨立套件）
- Produces:
  - `type Client struct{...}`
  - `type AuthError struct{ Err error }`；實作 `Error()` 與 `Unwrap()`
  - `type Option func(*Client)`
  - `func New(appKey, appSecret, refreshToken string, opts ...Option) *Client`
  - `func WithHTTPClient(h *http.Client) Option`
  - `func WithLogger(l *slog.Logger) Option`
  - `func WithEndpoints(token, upload, start, append, finish string) Option`
  - `func WithLimits(simpleLimit, chunkSize int64) Option`
  - `func (c *Client) Upload(ctx context.Context, localPath, remotePath string) error`（本階段僅 simple；>limit 暫回明確錯誤，Task 3 補上 session）

- [ ] **Step 1: 寫 client 測試（先失敗）**

`internal/dropbox/client_test.go`：

```go
package dropbox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func tokenHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		u, p, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "appkey", u)
		require.Equal(t, "appsecret", p)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "refresh_token", r.Form.Get("grant_type"))
		require.Equal(t, "reftok", r.Form.Get("refresh_token"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok123","token_type":"bearer","expires_in":14400}`))
	}
}

func TestUpload_Simple(t *testing.T) {
	var gotAuth, gotArg string
	var gotBody []byte
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", tokenHandler(t))
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotArg = r.Header.Get("Dropbox-API-Arg")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"app.log","id":"id:abc"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New("appkey", "appsecret", "reftok",
		WithEndpoints(srv.URL+"/oauth2/token", srv.URL+"/upload",
			srv.URL+"/start", srv.URL+"/append", srv.URL+"/finish"))

	dir := t.TempDir()
	fp := filepath.Join(dir, "app.log")
	require.NoError(t, os.WriteFile(fp, []byte("hello world"), 0o644))

	require.NoError(t, c.Upload(context.Background(), fp, "/logs/app.log"))
	require.Equal(t, "Bearer tok123", gotAuth)
	require.Contains(t, gotArg, `"path":"/logs/app.log"`)
	require.Contains(t, gotArg, `"mode":"overwrite"`)
	require.Equal(t, "hello world", string(gotBody))
}

func TestUpload_AuthError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New("k", "s", "r",
		WithEndpoints(srv.URL+"/oauth2/token", srv.URL+"/upload",
			srv.URL+"/start", srv.URL+"/append", srv.URL+"/finish"))

	dir := t.TempDir()
	fp := filepath.Join(dir, "a.log")
	require.NoError(t, os.WriteFile(fp, []byte("x"), 0o644))

	err := c.Upload(context.Background(), fp, "/logs/a.log")
	require.Error(t, err)
	var ae *AuthError
	require.ErrorAs(t, err, &ae)
}
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `go test ./internal/dropbox/...`
Expected: 編譯失敗（New / Upload / AuthError 尚未定義）

- [ ] **Step 3: 實作 client.go（token + simple upload）**

`internal/dropbox/client.go`：

```go
// Package dropbox 以標準庫 net/http 直接呼叫 Dropbox HTTP API，
// 提供 token 換取與檔案上傳（simple 與 session 分段）。
package dropbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const (
	defaultTokenURL         = "https://api.dropbox.com/oauth2/token"
	defaultUploadURL        = "https://content.dropboxapi.com/2/files/upload"
	defaultSessionStartURL  = "https://content.dropboxapi.com/2/files/upload_session/start"
	defaultSessionAppendURL = "https://content.dropboxapi.com/2/files/upload_session/append_v2"
	defaultSessionFinishURL = "https://content.dropboxapi.com/2/files/upload_session/finish"

	defaultSimpleLimit int64 = 150 * 1024 * 1024 // 150 MB
	defaultChunkSize   int64 = 8 * 1024 * 1024    // 8 MB（4MB 倍數）

	maxErrBody = 1 << 20 // 讀取錯誤回應的上限（1MB）
)

// AuthError 表示 token 換取（認證）階段的失敗，供呼叫端區分 exit code。
type AuthError struct{ Err error }

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// Client 為 Dropbox 上傳用戶端。
type Client struct {
	httpClient   *http.Client
	appKey       string
	appSecret    string
	refreshToken string
	logger       *slog.Logger

	simpleLimit int64
	chunkSize   int64

	tokenURL         string
	uploadURL        string
	sessionStartURL  string
	sessionAppendURL string
	sessionFinishURL string
}

// Option 為 Client 的選用設定。
type Option func(*Client)

// WithHTTPClient 覆寫底層 http.Client。
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

// WithLogger 設定結構化 logger。
func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.logger = l } }

// WithEndpoints 覆寫各 API endpoint（測試用）。
func WithEndpoints(token, upload, start, appendURL, finish string) Option {
	return func(c *Client) {
		c.tokenURL = token
		c.uploadURL = upload
		c.sessionStartURL = start
		c.sessionAppendURL = appendURL
		c.sessionFinishURL = finish
	}
}

// WithLimits 覆寫 simple 上限與分段大小（測試用）。
func WithLimits(simpleLimit, chunkSize int64) Option {
	return func(c *Client) {
		c.simpleLimit = simpleLimit
		c.chunkSize = chunkSize
	}
}

// New 建立 Client。未指定的選項採預設值。
func New(appKey, appSecret, refreshToken string, opts ...Option) *Client {
	c := &Client{
		httpClient:       &http.Client{}, // 不設全域 Timeout，改由 context 控制（支援大檔長傳）
		appKey:           appKey,
		appSecret:        appSecret,
		refreshToken:     refreshToken,
		logger:           slog.Default(),
		simpleLimit:      defaultSimpleLimit,
		chunkSize:        defaultChunkSize,
		tokenURL:         defaultTokenURL,
		uploadURL:        defaultUploadURL,
		sessionStartURL:  defaultSessionStartURL,
		sessionAppendURL: defaultSessionAppendURL,
		sessionFinishURL: defaultSessionFinishURL,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// accessToken 以 refresh token 換取短效 access token。失敗一律包成 *AuthError。
func (c *Client) accessToken(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", c.refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", &AuthError{fmt.Errorf("建立 token 請求失敗: %w", err)}
	}
	req.SetBasicAuth(c.appKey, c.appSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", &AuthError{fmt.Errorf("token 請求送出失敗: %w", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	if resp.StatusCode != http.StatusOK {
		return "", &AuthError{fmt.Errorf("token 換取失敗，狀態碼 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", &AuthError{fmt.Errorf("解析 token 回應失敗: %w", err)}
	}
	if tr.AccessToken == "" {
		return "", &AuthError{errors.New("token 回應未包含 access_token")}
	}
	return tr.AccessToken, nil
}

// commitInfo 為上傳/finish 共用的 commit 參數。
type commitInfo struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Mute bool   `json:"mute"`
}

// asciiSafe 將字串中非 ASCII 字元轉為 \uXXXX，確保可放入 HTTP header。
func asciiSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > 0x7F {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Upload 將 localPath 的檔案上傳到 remotePath（覆寫模式）。
func (c *Client) Upload(ctx context.Context, localPath, remotePath string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("讀取檔案資訊失敗: %w", err)
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	if info.Size() <= c.simpleLimit {
		return c.simpleUpload(ctx, token, localPath, remotePath)
	}
	return c.sessionUpload(ctx, token, localPath, remotePath, info.Size())
}

func (c *Client) simpleUpload(ctx context.Context, token, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("開啟檔案失敗: %w", err)
	}
	defer f.Close()

	arg, _ := json.Marshal(commitInfo{Path: remotePath, Mode: "overwrite", Mute: true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.uploadURL, f)
	if err != nil {
		return fmt.Errorf("建立上傳請求失敗: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Dropbox-API-Arg", asciiSafe(string(arg)))
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("上傳請求送出失敗: %w", err)
	}
	defer resp.Body.Close()
	return checkUploadResponse(resp)
}

func checkUploadResponse(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上傳失敗，狀態碼 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// sessionUpload 在 Task 3 實作。
func (c *Client) sessionUpload(ctx context.Context, token, localPath, remotePath string, size int64) error {
	return errors.New("session upload 尚未實作")
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `go test ./internal/dropbox/...`
Expected: PASS（TestUpload_Simple、TestUpload_AuthError）

- [ ] **Step 5: Commit**

```bash
git add internal/dropbox/
git commit -m "feat(dropbox): add token refresh and simple upload client"
```

---

### Task 3: dropbox client — session 分段上傳（大檔）

**Files:**
- Modify: `internal/dropbox/client.go`（補實作 `sessionUpload` 與 start/append/finish）
- Test: `internal/dropbox/client_test.go`（新增大檔測試）

**Interfaces:**
- Consumes: Task 2 的 `Client`、`commitInfo`、`asciiSafe`、`checkUploadResponse`、`maxErrBody`
- Produces:
  - `func (c *Client) sessionUpload(ctx context.Context, token, localPath, remotePath string, size int64) error`（真正實作）
  - 私有：`sessionStart`、`sessionAppend`、`sessionFinish`

- [ ] **Step 1: 新增大檔 session 測試（先失敗）**

於 `internal/dropbox/client_test.go` 追加（並在 import 補 `encoding/json`、`sort`）：

```go
func TestUpload_Session(t *testing.T) {
	chunks := map[uint64][]byte{}
	var commitPath string

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", tokenHandler(t))
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		chunks[0] = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session_id":"sess1"}`))
	})
	mux.HandleFunc("/append", func(w http.ResponseWriter, r *http.Request) {
		var a struct {
			Cursor struct {
				SessionID string `json:"session_id"`
				Offset    uint64 `json:"offset"`
			} `json:"cursor"`
		}
		require.NoError(t, json.Unmarshal([]byte(r.Header.Get("Dropbox-API-Arg")), &a))
		require.Equal(t, "sess1", a.Cursor.SessionID)
		b, _ := io.ReadAll(r.Body)
		chunks[a.Cursor.Offset] = b
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/finish", func(w http.ResponseWriter, r *http.Request) {
		var a struct {
			Cursor struct {
				Offset uint64 `json:"offset"`
			} `json:"cursor"`
			Commit struct {
				Path string `json:"path"`
				Mode string `json:"mode"`
			} `json:"commit"`
		}
		require.NoError(t, json.Unmarshal([]byte(r.Header.Get("Dropbox-API-Arg")), &a))
		b, _ := io.ReadAll(r.Body)
		if len(b) > 0 {
			chunks[a.Cursor.Offset] = b
		}
		commitPath = a.Commit.Path
		require.Equal(t, "overwrite", a.Commit.Mode)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"big.log"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New("appkey", "appsecret", "reftok",
		WithEndpoints(srv.URL+"/oauth2/token", srv.URL+"/upload",
			srv.URL+"/start", srv.URL+"/append", srv.URL+"/finish"),
		WithLimits(10, 4)) // simpleLimit=10 bytes, chunk=4 bytes → 走 session

	content := []byte("0123456789ABCDE") // 15 bytes > 10
	dir := t.TempDir()
	fp := filepath.Join(dir, "big.log")
	require.NoError(t, os.WriteFile(fp, content, 0o644))

	require.NoError(t, c.Upload(context.Background(), fp, "/logs/big.log"))

	// 依 offset 重組，確認內容完整且順序正確
	offsets := make([]uint64, 0, len(chunks))
	for k := range chunks {
		offsets = append(offsets, k)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	var assembled []byte
	for _, o := range offsets {
		assembled = append(assembled, chunks[o]...)
	}
	require.Equal(t, content, assembled)
	require.Equal(t, "/logs/big.log", commitPath)
}
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `go test ./internal/dropbox/... -run TestUpload_Session`
Expected: FAIL（"session upload 尚未實作"）

- [ ] **Step 3: 以實作取代 sessionUpload stub**

把 Task 2 client.go 末端的 `sessionUpload` stub 替換為下列內容，並在 import 補 `bytes`：

```go
type sessionStartResp struct {
	SessionID string `json:"session_id"`
}

type uploadCursor struct {
	SessionID string `json:"session_id"`
	Offset    uint64 `json:"offset"`
}

type appendArg struct {
	Cursor uploadCursor `json:"cursor"`
	Close  bool         `json:"close"`
}

type finishArg struct {
	Cursor uploadCursor `json:"cursor"`
	Commit commitInfo   `json:"commit"`
}

// sessionUpload 以分段 session 上傳大檔：start（第一段）→ append（中段）→ finish（空段 + commit）。
func (c *Client) sessionUpload(ctx context.Context, token, localPath, remotePath string, size int64) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("開啟檔案失敗: %w", err)
	}
	defer f.Close()

	buf := make([]byte, c.chunkSize)
	var offset uint64
	var sessionID string

	for {
		n, readErr := io.ReadFull(f, buf)
		if readErr == io.EOF {
			break // 無更多資料
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return fmt.Errorf("讀取分段失敗（offset=%d）: %w", offset, readErr)
		}
		chunk := buf[:n]
		isLast := offset+uint64(n) >= uint64(size)

		if sessionID == "" {
			sid, err := c.sessionStart(ctx, token, chunk)
			if err != nil {
				return err
			}
			sessionID = sid
		} else {
			if err := c.sessionAppend(ctx, token, sessionID, offset, chunk); err != nil {
				return err
			}
		}
		offset += uint64(n)
		if isLast {
			break
		}
	}

	return c.sessionFinish(ctx, token, sessionID, offset, remotePath)
}

func (c *Client) sessionStart(ctx context.Context, token string, data []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.sessionStartURL, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("建立 session start 請求失敗: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Dropbox-API-Arg", `{"close":false}`)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("session start 送出失敗: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("session start 失敗，狀態碼 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var sr sessionStartResp
	if err := json.Unmarshal(body, &sr); err != nil {
		return "", fmt.Errorf("解析 session start 回應失敗: %w", err)
	}
	if sr.SessionID == "" {
		return "", errors.New("session start 回應未包含 session_id")
	}
	return sr.SessionID, nil
}

func (c *Client) sessionAppend(ctx context.Context, token, sessionID string, offset uint64, data []byte) error {
	arg, _ := json.Marshal(appendArg{Cursor: uploadCursor{SessionID: sessionID, Offset: offset}, Close: false})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.sessionAppendURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("建立 session append 請求失敗: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Dropbox-API-Arg", asciiSafe(string(arg)))
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("session append 送出失敗（offset=%d）: %w", offset, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("session append 失敗（offset=%d），狀態碼 %d: %s", offset, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) sessionFinish(ctx context.Context, token, sessionID string, offset uint64, remotePath string) error {
	arg, _ := json.Marshal(finishArg{
		Cursor: uploadCursor{SessionID: sessionID, Offset: offset},
		Commit: commitInfo{Path: remotePath, Mode: "overwrite", Mute: true},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.sessionFinishURL, bytes.NewReader(nil))
	if err != nil {
		return fmt.Errorf("建立 session finish 請求失敗: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Dropbox-API-Arg", asciiSafe(string(arg)))
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("session finish 送出失敗: %w", err)
	}
	defer resp.Body.Close()
	return checkUploadResponse(resp)
}
```

- [ ] **Step 4: 執行全套測試確認通過**

Run: `go test ./...`
Expected: PASS（config 4 個 + dropbox 3 個）

- [ ] **Step 5: Commit**

```bash
git add internal/dropbox/
git commit -m "feat(dropbox): add chunked upload session for large files"
```

---

### Task 4: main.go 整合 + build.sh + 範本 + README

**Files:**
- Create: `main.go`
- Create: `build.sh`
- Create: `secrets.env.example`
- Create: `README.md`

**Interfaces:**
- Consumes: `config.Load`、`config.RemotePath`、`dropbox.New`、`dropbox.Client.Upload`、`dropbox.AuthError`

- [ ] **Step 1: 實作 main.go**

`main.go`：

```go
// Command dropbox-uploader 把指定檔案上傳備份到固定的 Dropbox App folder。
// 機密於 build 時以 -ldflags 注入 internal/config。
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/gilbertchiao/dropbox-uploader/internal/config"
	"github.com/gilbertchiao/dropbox-uploader/internal/dropbox"
)

const (
	exitOK        = 0
	exitUsage     = 1
	exitFileError = 2
	exitConfigErr = 3
	exitAuthError = 4
	exitUploadErr = 5

	uploadTimeout = 30 * time.Minute
)

func main() {
	os.Exit(run(os.Args))
}

func run(args []string) int {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if len(args) != 2 {
		logger.Error("用法錯誤", "usage", "dropbox-uploader <檔案路徑>")
		return exitUsage
	}
	localPath := args[1]

	cfg, err := config.Load()
	if err != nil {
		logger.Error("設定載入失敗", "err", err)
		return exitConfigErr
	}

	info, err := os.Stat(localPath)
	if err != nil {
		logger.Error("無法存取檔案", "path", localPath, "err", err)
		return exitFileError
	}
	if info.IsDir() {
		logger.Error("目標是目錄而非檔案", "path", localPath)
		return exitFileError
	}

	remotePath, err := cfg.RemotePath(localPath)
	if err != nil {
		logger.Error("無法組出遠端路徑", "err", err)
		return exitFileError
	}

	client := dropbox.New(cfg.AppKey, cfg.AppSecret, cfg.RefreshToken, dropbox.WithLogger(logger))

	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()

	start := time.Now()
	logger.Info("開始上傳", "local", localPath, "remote", remotePath, "size", info.Size())
	if err := client.Upload(ctx, localPath, remotePath); err != nil {
		var authErr *dropbox.AuthError
		if errors.As(err, &authErr) {
			logger.Error("認證失敗", "err", err)
			return exitAuthError
		}
		logger.Error("上傳失敗", "err", err)
		return exitUploadErr
	}
	logger.Info("上傳完成", "remote", remotePath, "elapsed", time.Since(start).String())
	return exitOK
}
```

- [ ] **Step 2: 驗證可編譯（不需注入也要能 build）**

Run: `go build ./... && go vet ./...`
Expected: 無錯誤輸出

- [ ] **Step 3: 建立 secrets.env.example**

`secrets.env.example`：

```bash
# 複製本檔為 secrets.env 並填入實際值。
# secrets.env 已列入 .gitignore，不會進版控。
DROPBOX_APP_KEY=
DROPBOX_APP_SECRET=
DROPBOX_REFRESH_TOKEN=
DROPBOX_TARGET_DIR=/logs
```

- [ ] **Step 4: 建立 build.sh（--go 風格）**

`build.sh`：

```bash
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

LDFLAGS="-s -w"
LDFLAGS+=" -X ${PKG}.AppKey=${DROPBOX_APP_KEY}"
LDFLAGS+=" -X ${PKG}.AppSecret=${DROPBOX_APP_SECRET}"
LDFLAGS+=" -X ${PKG}.RefreshToken=${DROPBOX_REFRESH_TOKEN}"
LDFLAGS+=" -X ${PKG}.TargetDir=${DROPBOX_TARGET_DIR}"

if $DRY_RUN; then
  echo "[dry-run] 將執行：CGO_ENABLED=0 go build -trimpath -ldflags \"<已隱藏機密>\" -o ${OUTPUT} ."
  echo "[dry-run] 加上 --go 參數才會真正編譯。"
  exit 0
fi

echo "編譯中…"
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$OUTPUT" .
echo "完成：${OUTPUT}"
```

執行：`chmod +x build.sh`

- [ ] **Step 5: 驗證 build.sh dry-run 與實際編譯**

```bash
chmod +x build.sh
./build.sh                        # 應提示缺 secrets.env 並 exit 1
cp secrets.env.example secrets.env
# 暫填假值供 build 驗證
sed -i 's/DROPBOX_APP_KEY=/DROPBOX_APP_KEY=dummy/; s/DROPBOX_APP_SECRET=/DROPBOX_APP_SECRET=dummy/; s/DROPBOX_REFRESH_TOKEN=/DROPBOX_REFRESH_TOKEN=dummy/' secrets.env
./build.sh                        # dry-run，顯示將執行的指令
./build.sh --go                   # 真正編譯，產出 dropbox-uploader
./dropbox-uploader                # 無參數 → exit code 1（用法錯誤）
rm -f secrets.env dropbox-uploader
```
Expected: dry-run 正常、`--go` 產出 binary、無參數時印用法錯誤。

- [ ] **Step 6: 建立 README.md**

`README.md`（重點段落）：

```markdown
# dropbox-uploader

把指定檔案（主要是 log）上傳備份到固定的 Dropbox App folder 的單一靜態執行檔。

## 用法

    dropbox-uploader <檔案路徑>

## 建置

1. 於 Dropbox App Console 建立一個「App folder」類型的 app，取得 App key / App secret，
   並透過 OAuth（`token_access_type=offline`）取得 refresh token。
2. 複製範本並填值：

       cp secrets.env.example secrets.env
       # 編輯 secrets.env 填入四個值

3. 編譯（機密於此步驟以 ldflags 注入 binary）：

       ./build.sh --go

   產出的 `dropbox-uploader` 可直接部署到 server，無需設定檔。

## 安全性說明

機密被編入 binary，並非加密；能讀到 binary 者可用 `strings` 取出。請務必使用
「App folder」權限類型，將外洩風險限縮於該備份資料夾。`secrets.env` 不進版控。

## Exit code

| code | 意義 |
| ---- | ---- |
| 0 | 成功 |
| 1 | 參數錯誤 |
| 2 | 檔案問題 |
| 3 | 設定錯誤（注入機密缺失）|
| 4 | 認證失敗 |
| 5 | 上傳失敗 |
```

- [ ] **Step 7: 最終驗證與 Commit**

```bash
go test ./...
git add main.go build.sh secrets.env.example README.md
git commit -m "feat: add CLI entrypoint, build script, and docs"
```
Expected: 全部測試通過。

---

## Self-Review

- **Spec coverage**：單一 binary（Task 4）、ldflags 注入（Task 1 變數 + Task 4 build.sh）、net/http 零相依（Task 2/3）、App folder 路徑（Task 1 RemotePath）、simple/​session 上傳（Task 2/3）、overwrite（Task 2/3）、slog + 錯誤包裝（全程）、exit code（Task 4）、測試（Task 1/2/3）、build.sh --go 風格（Task 4）、安全取捨（README）。皆有對應。
- **Placeholder scan**：除 Task 2 刻意的 `sessionUpload` stub（Task 3 取代）外無未完成項。
- **Type consistency**：`commitInfo` 於 simple 與 finish 共用；`uploadCursor`/`appendArg`/`finishArg` 一致；`AuthError` 於 accessToken 產生、main 以 `errors.As` 偵測；endpoint 與 limit 皆可由 Option 覆寫供測試。
