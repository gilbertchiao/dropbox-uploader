package dropbox

import (
	"context"
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
