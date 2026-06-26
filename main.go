// Command dropbox-uploader 把指定檔案上傳備份到固定的 Dropbox App folder。
// 機密於 build 時以 -ldflags 注入 internal/config，部署時只需單一執行檔、無設定檔。
package main

import (
	"context"
	"errors"
	"fmt"
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

func main() {
	os.Exit(run(os.Args))
}

// run 為實際流程，回傳 process exit code，方便測試。
func run(args []string) int {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if len(args) == 2 && (args[1] == "--version" || args[1] == "-v") {
		fmt.Println(version)
		return exitOK
	}

	if len(args) != 2 {
		logger.Error("用法錯誤", "usage", "dropbox-uploader <檔案路徑> | --version")
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
