package server

import (
	"testing"
	"time"

	"rss-ai/internal/config"
)

func TestAuthToken(t *testing.T) {
	old := appConfig
	defer func() { appConfig = old }()
	appConfig = &config.Config{}
	appConfig.Server.Password = "secret"

	token := signToken(time.Now())
	if !verifyToken(token) {
		t.Fatal("fresh token should verify")
	}
	if verifyToken(token + "x") {
		t.Error("tampered token should fail")
	}
	if verifyToken("1234567890.AAAA") {
		t.Error("forged token should fail")
	}

	// 已过期的令牌
	expired := signToken(time.Now().Add(-authSessionMaxAge - time.Minute))
	if verifyToken(expired) {
		t.Error("expired token should fail")
	}

	// 修改密码后旧会话失效
	appConfig.Server.Password = "changed"
	if verifyToken(token) {
		t.Error("token should be invalid after password change")
	}
}
