// Package dropbox 以標準庫 net/http 直接呼叫 Dropbox HTTP API，
// 提供 token 換取與檔案上傳（simple 與 session 分段）。
package dropbox

import (
	"bytes"
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
	"unicode/utf16"
)

const (
	defaultTokenURL         = "https://api.dropbox.com/oauth2/token"
	defaultUploadURL        = "https://content.dropboxapi.com/2/files/upload"
	defaultSessionStartURL  = "https://content.dropboxapi.com/2/files/upload_session/start"
	defaultSessionAppendURL = "https://content.dropboxapi.com/2/files/upload_session/append_v2"
	defaultSessionFinishURL = "https://content.dropboxapi.com/2/files/upload_session/finish"

	defaultSimpleLimit int64 = 150 * 1024 * 1024 // 150 MB
	defaultChunkSize   int64 = 8 * 1024 * 1024   // 8 MB（4MB 倍數）

	maxErrBody = 1 << 20 // 讀取回應內容的上限（1MB）
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

// WithHTTPClient 覆寫底層 http.Client。傳入 nil 時忽略，避免 Do 時 panic。
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithLogger 設定結構化 logger。
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) {
		if l != nil {
			c.logger = l
		}
	}
}

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

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	if readErr != nil {
		return "", &AuthError{fmt.Errorf("讀取 token 回應失敗: %w", readErr)}
	}
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

// asciiSafe 將字串中非 ASCII 字元轉為 JSON \uXXXX 轉義，確保可放入 HTTP header。
// Dropbox-API-Arg header 僅接受 ASCII，含中文等字元的路徑需先轉義。
// 非 BMP 字元（如 emoji，> U+FFFF）以 UTF-16 surrogate pair（兩個 \uXXXX）輸出，
// 才能維持合法的 JSON 轉義。
func asciiSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r <= 0x7F:
			b.WriteRune(r)
		case r <= 0xFFFF:
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&b, "\\u%04x\\u%04x", r1, r2)
		}
	}
	return b.String()
}

// Upload 將 localPath 的檔案上傳到 remotePath（覆寫模式）。
// 檔案大小不超過 simpleLimit 時走 simple upload，否則走分段 session。
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
		c.logger.Debug("採用 simple upload", "size", info.Size())
		return c.simpleUpload(ctx, token, localPath, remotePath)
	}
	c.logger.Debug("採用 session upload", "size", info.Size(), "chunk", c.chunkSize)
	return c.sessionUpload(ctx, token, localPath, remotePath, info.Size())
}

func (c *Client) simpleUpload(ctx context.Context, token, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("開啟檔案失敗: %w", err)
	}
	defer f.Close()

	arg, err := json.Marshal(commitInfo{Path: remotePath, Mode: "overwrite", Mute: true})
	if err != nil {
		return fmt.Errorf("組裝上傳參數失敗: %w", err)
	}
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

// checkUploadResponse 檢查上傳類請求的回應狀態。
// 讀取回應失敗時回傳錯誤，避免在無法確認結果時誤判為成功。
func checkUploadResponse(resp *http.Response) error {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	if readErr != nil {
		return fmt.Errorf("讀取上傳回應失敗（狀態碼 %d）: %w", resp.StatusCode, readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上傳失敗，狀態碼 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

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

// sessionUpload 以分段 session 上傳大檔：
// start（第一段）→ append（中段）→ finish（空段 + commit）。
func (c *Client) sessionUpload(ctx context.Context, token, localPath, remotePath string, size int64) error {
	if size <= 0 {
		return fmt.Errorf("session upload 的檔案大小無效: %d", size)
	}
	if c.chunkSize <= 0 {
		return fmt.Errorf("分段大小無效: %d", c.chunkSize)
	}

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
			break // 已無更多資料
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return fmt.Errorf("讀取分段失敗（offset=%d）: %w", offset, readErr)
		}
		chunk := buf[:n]
		isLast := offset+uint64(n) >= uint64(size)

		if sessionID == "" {
			sid, startErr := c.sessionStart(ctx, token, chunk)
			if startErr != nil {
				return startErr
			}
			sessionID = sid
		} else {
			if appendErr := c.sessionAppend(ctx, token, sessionID, offset, chunk); appendErr != nil {
				return appendErr
			}
		}
		offset += uint64(n)
		if isLast {
			break
		}
	}

	// 若檔案在 Stat 後被清空/截斷導致完全沒讀到資料，session 不會建立。
	if sessionID == "" {
		return errors.New("沒有讀到任何資料，session 未建立（檔案可能已被清空）")
	}

	// 所有資料已上傳，finish 以空段在最終 offset 提交。
	return c.sessionFinish(ctx, token, sessionID, offset, remotePath)
}

// sessionStart 開啟一個 upload session 並上傳第一段，回傳 session_id。
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

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	if readErr != nil {
		return "", fmt.Errorf("讀取 session start 回應失敗: %w", readErr)
	}
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

// sessionAppend 將一段資料附加到既有 session 的指定 offset。
func (c *Client) sessionAppend(ctx context.Context, token, sessionID string, offset uint64, data []byte) error {
	arg, err := json.Marshal(appendArg{Cursor: uploadCursor{SessionID: sessionID, Offset: offset}, Close: false})
	if err != nil {
		return fmt.Errorf("組裝 session append 參數失敗: %w", err)
	}
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

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	if readErr != nil {
		return fmt.Errorf("讀取 session append 回應失敗（offset=%d）: %w", offset, readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("session append 失敗（offset=%d），狀態碼 %d: %s", offset, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// sessionFinish 以空段在最終 offset 提交 session，完成上傳。
func (c *Client) sessionFinish(ctx context.Context, token, sessionID string, offset uint64, remotePath string) error {
	arg, err := json.Marshal(finishArg{
		Cursor: uploadCursor{SessionID: sessionID, Offset: offset},
		Commit: commitInfo{Path: remotePath, Mode: "overwrite", Mute: true},
	})
	if err != nil {
		return fmt.Errorf("組裝 session finish 參數失敗: %w", err)
	}
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
