# dropbox-uploader

把指定檔案上傳備份到固定 Dropbox App folder 的單一靜態執行檔。零外部相依、部署時只需一個 binary、無設定檔。

## 用法

```bash
dropbox-uploader <檔案路徑>
# 例：dropbox-uploader /var/log/myapp/app.log

dropbox-uploader --version   # 顯示版本（亦可用 -v）
```

檔案會以原檔名上傳到 build 時設定的目標資料夾，同名直接覆寫。

版本由 `build.sh` 於編譯時取自 git tag（如 `v1.0.0`）注入；未經 `build.sh`（例如直接 `go build`）編譯時顯示為 `dev`。

## 建置

1. 於 [Dropbox App Console](https://www.dropbox.com/developers/apps) 建立一個 **App folder** 類型的 app，取得 App key / App secret，並透過 OAuth（`token_access_type=offline`）取得 refresh token。
2. 複製範本並填值：

   ```bash
   cp secrets.env.example secrets.env
   # 編輯 secrets.env，填入四個值
   ```

3. 編譯（機密於此步驟以 ldflags 注入 binary）：

   ```bash
   ./build.sh --go
   ```

   `build.sh` 預設為 dry-run（只顯示將執行的指令），加上 `--go` 才真正編譯。產出的 `dropbox-uploader` 可直接部署到 server。

## 取得 refresh token（一次性）

App folder app 建立後，依序在瀏覽器與終端機操作：

1. 開啟（換成你的 App key）：
   `https://www.dropbox.com/oauth2/authorize?client_id=<APP_KEY>&token_access_type=offline&response_type=code`
2. 授權後取得 authorization code，換取 refresh token：

   ```bash
   curl https://api.dropbox.com/oauth2/token \
     -d code=<AUTH_CODE> \
     -d grant_type=authorization_code \
     -u <APP_KEY>:<APP_SECRET>
   ```

   回應中的 `refresh_token` 即為要填入 `secrets.env` 的值。

## 安全性說明

機密被編入 binary，**並非加密**；能讀到 binary 者可用 `strings` 取出。請務必使用 **App folder** 權限類型，將外洩風險限縮於該備份資料夾，避免使用 Full Dropbox。`secrets.env` 不進版控，僅保存在開發機。

## Exit code

| code | 意義                         |
| ---- | ---------------------------- |
| 0    | 成功                         |
| 1    | 參數錯誤（缺檔名 / 用法錯）  |
| 2    | 檔案問題（不存在 / 是目錄） |
| 3    | 設定錯誤（注入機密缺失）    |
| 4    | 認證失敗（token 換取失敗）  |
| 5    | 上傳失敗（API 回錯 / 網路） |

## 開發

```bash
go test ./...   # 執行單元測試
go vet ./...
```

設計與實作文件見 `docs/superpowers/`。

## License

[MIT](LICENSE) © 2026 Gilbert Chiao
