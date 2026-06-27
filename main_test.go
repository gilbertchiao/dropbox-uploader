package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestRun_Version(t *testing.T) {
	for _, flag := range []string{"--version", "-v"} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			// 擷取 stdout 驗證印出的內容為 version 變數值。
			orig := os.Stdout
			r, w, err := os.Pipe()
			require.NoError(t, err)
			// 以 defer 確保即使斷言失敗（FailNow）也會還原 os.Stdout 並釋放 fd。
			// w 也一併關閉（顯式 w.Close() 之外的保險，double-close 無妨）。
			defer func() {
				os.Stdout = orig
				_ = w.Close()
				_ = r.Close()
			}()

			os.Stdout = w
			code := run([]string{"dropbox-uploader", flag})
			require.NoError(t, w.Close())

			out, err := io.ReadAll(r)
			require.NoError(t, err)

			require.Equal(t, exitOK, code, "flag %s 應回傳成功", flag)
			require.Equal(t, version, strings.TrimSpace(string(out)), "flag %s 應印出版本", flag)
		})
	}
}

func TestRun_Help(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			// 擷取 stdout 驗證 -h/--help 會印出用法說明且成功結束。
			orig := os.Stdout
			r, w, err := os.Pipe()
			require.NoError(t, err)
			defer func() {
				os.Stdout = orig
				_ = w.Close()
				_ = r.Close()
			}()

			os.Stdout = w
			code := run([]string{"dropbox-uploader", flag})
			require.NoError(t, w.Close())

			out, err := io.ReadAll(r)
			require.NoError(t, err)

			require.Equal(t, exitOK, code, "flag %s 應回傳成功", flag)
			require.Contains(t, string(out), "用法", "flag %s 應印出用法說明", flag)
			require.Contains(t, string(out), "dropbox-uploader")
		})
	}
}

func TestRun_Usage(t *testing.T) {
	// 參數數量錯誤在 config 載入前就回傳，與 config 狀態無關
	require.Equal(t, exitUsage, run([]string{"dropbox-uploader"}))
	require.Equal(t, exitUsage, run([]string{"dropbox-uploader", "a", "b"}))
}

func TestRun_NoArgsShowsUsageNotErrorLog(t *testing.T) {
	// 無參數時應顯示乾淨的用法說明（寫到 stderr），而非 slog 錯誤 log，但仍回傳 exitUsage。
	orig := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() {
		os.Stderr = orig
		_ = w.Close()
		_ = r.Close()
	}()

	os.Stderr = w
	code := run([]string{"dropbox-uploader"})
	require.NoError(t, w.Close())

	out, err := io.ReadAll(r)
	require.NoError(t, err)

	require.Equal(t, exitUsage, code)
	require.Contains(t, string(out), "用法", "無參數應顯示用法說明")
	require.NotContains(t, string(out), "level=ERROR", "無參數不應再輸出 slog 錯誤 log")
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
