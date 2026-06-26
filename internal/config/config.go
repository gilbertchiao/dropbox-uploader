// Package config 提供 build 時透過 ldflags 注入的設定值與其驗證。
package config

import (
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
	if base == "." || base == ".." || base == string(filepath.Separator) || base == "" {
		return "", fmt.Errorf("無法從檔名取得有效的 basename: %q", filename)
	}
	// TargetDir 去除前後斜線後再補單一 leading slash；
	// 當 TargetDir 為根目錄（"/" 或全斜線）時 trimmed 為空，dir 留空避免組出 "//base"。
	trimmed := strings.Trim(c.TargetDir, "/")
	dir := ""
	if trimmed != "" {
		dir = "/" + trimmed
	}
	return dir + "/" + base, nil
}
