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

func TestRemotePath_RootTargetDir(t *testing.T) {
	c := &Config{TargetDir: "/"}
	got, err := c.RemotePath("app.log")
	require.NoError(t, err)
	require.Equal(t, "/app.log", got) // 不應組出 "//app.log"
}

func TestRemotePath_InvalidBasenames(t *testing.T) {
	c := &Config{TargetDir: "/logs"}
	for _, name := range []string{".", "..", "/", ""} {
		_, err := c.RemotePath(name)
		require.Error(t, err, "basename %q 應被拒絕", name)
	}
}

// setInjected 暫時設定 ldflags 注入的套件變數，並回傳還原函式。
func setInjected(t *testing.T, key, secret, refresh, dir string) func() {
	t.Helper()
	origKey, origSecret, origRefresh, origDir := AppKey, AppSecret, RefreshToken, TargetDir
	AppKey, AppSecret, RefreshToken, TargetDir = key, secret, refresh, dir
	return func() {
		AppKey, AppSecret, RefreshToken, TargetDir = origKey, origSecret, origRefresh, origDir
	}
}

func TestLoad_Success(t *testing.T) {
	restore := setInjected(t, "  key  ", "secret", "refresh", " /logs ")
	defer restore()

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "key", cfg.AppKey) // 確認 TrimSpace 生效
	require.Equal(t, "/logs", cfg.TargetDir)
}

func TestLoad_MissingFields(t *testing.T) {
	restore := setInjected(t, "key", "", "", "")
	defer restore()

	_, err := Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "AppSecret")
}
