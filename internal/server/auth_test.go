package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rss-ai/internal/config"
)

func TestAuthToken(t *testing.T) {
	old := appConfig
	defer func() { appConfig = old }()
	appConfig = &config.Config{}
	appConfig.Server.Password = "secret"

	token := signToken(time.Now(), true)
	if valid, isAdmin := verifyToken(token); !valid || !isAdmin {
		t.Fatalf("fresh admin token should verify as admin, got (%v, %v)", valid, isAdmin)
	}
	if valid, _ := verifyToken(token + "x"); valid {
		t.Error("tampered token should fail")
	}
	if valid, _ := verifyToken("1234567890.AAAA"); valid {
		t.Error("forged legacy-format token should fail")
	}

	// 访客令牌：级别 r，verify 应返回非管理员
	readerToken := signToken(time.Now(), false)
	if valid, isAdmin := verifyToken(readerToken); !valid || isAdmin {
		t.Errorf("reader token should verify as non-admin, got (%v, %v)", valid, isAdmin)
	}

	// 已过期的令牌
	expired := signToken(time.Now().Add(-authSessionMaxAge-time.Minute), true)
	if valid, _ := verifyToken(expired); valid {
		t.Error("expired token should fail")
	}

	// 修改密码后旧会话失效（两密码共同参与会话密钥派生）
	appConfig.Server.Password = "changed"
	if valid, _ := verifyToken(token); valid {
		t.Error("token should be invalid after password change")
	}
	if valid, _ := verifyToken(readerToken); valid {
		t.Error("reader token should be invalid after password change")
	}
}

// 双密码登录分级：管理员密码进管理态、阅读密码进访客态、AdminMiddleware 拦截访客
func TestDualPasswordAuth(t *testing.T) {
	old := appConfig
	defer func() { appConfig = old }()
	appConfig = &config.Config{}
	appConfig.Server.Password = "admin-pwd"
	appConfig.Server.ReaderPassword = "reader-pwd"

	// 管理员登录
	w := httptest.NewRecorder()
	LoginSubmit(w, stringBody("password=admin-pwd"))
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Fatalf("admin login should redirect to /, got %q", loc)
	}
	adminCookie := w.Header().Get("Set-Cookie")

	// 访客登录
	w = httptest.NewRecorder()
	LoginSubmit(w, stringBody("password=reader-pwd"))
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Fatalf("reader login should redirect to /, got %q", loc)
	}
	readerCookie := w.Header().Get("Set-Cookie")

	// 错误密码
	w = httptest.NewRecorder()
	LoginSubmit(w, stringBody("password=wrong"))
	if loc := w.Header().Get("Location"); loc != "/login?error=1" {
		t.Fatalf("wrong password should redirect to error, got %q", loc)
	}

	// authLevel 分级
	if lvl := authLevel(withCookie(adminCookie)); lvl != authLevelAdmin {
		t.Errorf("admin cookie level = %d, want 2", lvl)
	}
	if lvl := authLevel(withCookie(readerCookie)); lvl != authLevelReader {
		t.Errorf("reader cookie level = %d, want 1", lvl)
	}

	// AdminMiddleware：访客访问管理 API 403，管理员放行
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	rr := httptest.NewRecorder()
	AdminMiddleware(okHandler).ServeHTTP(rr, httptest.NewRequest("POST", "/api/feeds", nil))
	if rr.Code != http.StatusForbidden {
		t.Errorf("anonymous admin API call = %d, want 403", rr.Code)
	}

	req := httptest.NewRequest("POST", "/api/feeds", nil)
	req.Header.Set("Cookie", readerCookie)
	rr = httptest.NewRecorder()
	AdminMiddleware(okHandler).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("reader admin API call = %d, want 403", rr.Code)
	}

	req = httptest.NewRequest("POST", "/api/feeds", nil)
	req.Header.Set("Cookie", adminCookie)
	rr = httptest.NewRecorder()
	AdminMiddleware(okHandler).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("admin API call = %d, want 200", rr.Code)
	}
}

// 未启用登录（password 空）时全员视为管理员：AdminMiddleware 直接放行
func TestAuthDisabledAdminBypass(t *testing.T) {
	old := appConfig
	defer func() { appConfig = old }()
	appConfig = &config.Config{}

	if lvl := authLevel(httptest.NewRequest("GET", "/", nil)); lvl != authLevelAdmin {
		t.Errorf("auth disabled level = %d, want 2", lvl)
	}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	rr := httptest.NewRecorder()
	AdminMiddleware(okHandler).ServeHTTP(rr, httptest.NewRequest("POST", "/api/settings", nil))
	if rr.Code != 200 {
		t.Errorf("auth disabled admin API call = %d, want 200", rr.Code)
	}
}

// stringBody 构造 application/x-www-form-urlencoded POST 请求
func stringBody(form string) *http.Request {
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// withCookie 从 Set-Cookie 头提取 cookie 名值对塞回请求
func withCookie(setCookie string) *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	if kv := strings.Split(setCookie, ";")[0]; kv != "" {
		req.Header.Set("Cookie", kv)
	}
	return req
}
