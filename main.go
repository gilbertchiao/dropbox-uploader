// Command dropbox-uploader 把指定檔案上傳備份到固定的 Dropbox App folder。
// 機密於 build 時以 -ldflags 注入 internal/config，部署時只需單一執行檔、無設定檔。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/gilbertchiao/dropbox-uploader/internal/config"
	"github.com/gilbertchiao/dropbox-uploader/internal/dropbox"
)

// version 由 build 時以 -ldflags "-X main.version=..." 注入（build.sh 取自 git describe）；
// 未注入時預設為 dev。
var version = "dev"

const (
	exitOK        = 0
	exitUsage     = 1
	exitFileError = 2
	exitConfigErr = 3
	exitAuthError = 4
	exitUploadErr = 5

	uploadTimeout = 30 * time.Minute
)

// usageText 為執行檔的用法說明，於 -h/--help 或參數錯誤時顯示。
const usageText = `dropbox-uploader — 把指定檔案上傳備份到固定的 Dropbox 資料夾

用法：
  dropbox-uploader <檔案路徑>    上傳指定檔案到 build 時設定的目標資料夾（同名直接覆寫）
  dropbox-uploader --help        顯示本說明（亦可用 -h）
  dropbox-uploader --version     顯示版本（亦可用 -v）

範例：
  dropbox-uploader /var/log/myapp/app.log

說明：
  app key/secret、refresh token 與目標資料夾於 build 時以 -ldflags 注入，
  部署時只需這個單一執行檔、無需設定檔。
`

func main() {
	os.Exit(run(os.Args))
}

// printUsage 將用法說明寫到指定 writer：
//   - -h/--help 為使用者主動查詢，寫到 os.Stdout（正常輸出）
//   - 參數錯誤屬於錯誤情境，寫到 os.Stderr（避免污染 stdout）
func printUsage(w io.Writer) {
	fmt.Fprint(w, usageText)
}

// run 為實際流程，回傳 process exit code，方便測試。
func run(args []string) int {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 先處理單一旗標：--version/-v 與 --help/-h，皆視為成功結束。
	if len(args) == 2 {
		switch args[1] {
		case "--version", "-v":
			fmt.Println(version)
			return exitOK
		case "--help", "-h":
			printUsage(os.Stdout)
			return exitOK
		}
	}

	// 無參數或參數數量不對：顯示用法說明（非錯誤 log），
	// 但仍回傳 exitUsage，讓 cron/script 能由 exit code 偵測到漏傳參數。
	if len(args) != 2 {
		printUsage(os.Stderr)
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
