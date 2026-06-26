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
