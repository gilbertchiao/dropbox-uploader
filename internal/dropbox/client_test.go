package dropbox

import (
	"context"
	"encoding/json"
	"errors"
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

func TestUpload_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", tokenHandler(t))
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error_summary":"path/conflict/..."}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New("appkey", "appsecret", "reftok",
		WithEndpoints(srv.URL+"/oauth2/token", srv.URL+"/upload",
			srv.URL+"/start", srv.URL+"/append", srv.URL+"/finish"))

	dir := t.TempDir()
	fp := filepath.Join(dir, "a.log")
	require.NoError(t, os.WriteFile(fp, []byte("x"), 0o644))

	err := c.Upload(context.Background(), fp, "/logs/a.log")
	require.Error(t, err)
	// 上傳階段錯誤不應被歸為認證錯誤
	var ae *AuthError
	require.False(t, errors.As(err, &ae))
}

func TestUpload_NonASCIIPath(t *testing.T) {
	var gotArg string
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", tokenHandler(t))
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		gotArg = r.Header.Get("Dropbox-API-Arg")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"file.log"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New("appkey", "appsecret", "reftok",
		WithEndpoints(srv.URL+"/oauth2/token", srv.URL+"/upload",
			srv.URL+"/start", srv.URL+"/append", srv.URL+"/finish"))

	dir := t.TempDir()
	fp := filepath.Join(dir, "local.txt")
	require.NoError(t, os.WriteFile(fp, []byte("x"), 0o644))

	require.NoError(t, c.Upload(context.Background(), fp, "/紀錄/檔案.log"))
	// Dropbox-API-Arg header 必須只含 ASCII：中文需轉義為 \uXXXX，不可出現原始中文
	require.NotContains(t, gotArg, "紀錄")
	require.Contains(t, gotArg, `\u`)
	for _, r := range gotArg {
		require.LessOrEqual(t, r, rune(0x7F), "header 應只含 ASCII 字元")
	}
}

func TestAsciiSafe(t *testing.T) {
	for _, in := range []string{"abc", "紀錄", "a😀b", "/logs/app.log"} {
		out := asciiSafe(in)
		for _, r := range out {
			require.LessOrEqual(t, r, rune(0x7F), "輸出應只含 ASCII: %q", out)
		}
		// 將輸出當成 JSON 字串解碼應還原原字串，藉此驗證 \uXXXX 與 surrogate pair 合法
		var decoded string
		require.NoError(t, json.Unmarshal([]byte(`"`+out+`"`), &decoded))
		require.Equal(t, in, decoded)
	}
}
