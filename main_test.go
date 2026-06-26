package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gilbertchiao/dropbox-uploader/internal/config"
	"github.com/stretchr/testify/require"
)

// setConfig 暫時設定 config 套件的注入變數，回傳還原函式。
func setConfig(t *testing.T, key, secret, refresh, dir string) func() {
	t.Helper()
	ok, os2, or, od := config.AppKey, config.AppSecret, config.RefreshToken, config.TargetDir
	config.AppKey, config.AppSecret, config.RefreshToken, config.TargetDir = key, secret, refresh, dir
	return func() {
		config.AppKey, config.AppSecret, config.RefreshToken, config.TargetDir = ok, os2, or, od
	}
}

func TestRun_Usage(t *testing.T) {
	// 參數數量錯誤在 config 載入前就回傳，與 config 狀態無關
	require.Equal(t, exitUsage, run([]string{"dropbox-uploader"}))
	require.Equal(t, exitUsage, run([]string{"dropbox-uploader", "a", "b"}))
}

func TestRun_ConfigError(t *testing.T) {
	restore := setConfig(t, "", "", "", "") // 機密缺失
	defer restore()

	dir := t.TempDir()
	fp := filepath.Join(dir, "app.log")
	require.NoError(t, os.WriteFile(fp, []byte("x"), 0o644))

	require.Equal(t, exitConfigErr, run([]string{"dropbox-uploader", fp}))
}

func TestRun_FileNotFound(t *testing.T) {
	restore := setConfig(t, "k", "s", "r", "/logs") // config 完整，讓流程進到檔案檢查
	defer restore()

	require.Equal(t, exitFileError, run([]string{"dropbox-uploader", "/nonexistent/path/file.log"}))
}

func TestRun_FileIsDirectory(t *testing.T) {
	restore := setConfig(t, "k", "s", "r", "/logs")
	defer restore()

	dir := t.TempDir() // 傳入目錄而非檔案
	require.Equal(t, exitFileError, run([]string{"dropbox-uploader", dir}))
}
