package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	authCookieName     = "rss_ai_auth"
	authSessionMaxAge  = 7 * 24 * time.Hour // 登录会话有效期
	authLoginFailDelay = time.Second        // 密码错误延迟，缓解暴力破解
)

// authEnabled 是否启用登录校验（server.password 留空则关闭）
func authEnabled() bool {
	return appConfig != nil && appConfig.Server.Password != ""
}

// sessionKey 会话签名密钥由当前密码派生：修改密码后旧会话全部自动失效
func sessionKey() []byte {
	sum := sha256.Sum256([]byte("rss-ai:" + appConfig.Server.Password))
	return sum[:]
}

// signToken 生成 "过期时间戳.签名" 形式的会话令牌
func signToken(now time.Time) string {
	msg := strconv.FormatInt(now.Add(authSessionMaxAge).Unix(), 10)
	mac := hmac.New(sha256.New, sessionKey())
	mac.Write([]byte(msg))
	return msg + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyToken 校验会话令牌（未过期且签名匹配）
func verifyToken(token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return false
	}
	mac := hmac.New(sha256.New, sessionKey())
	mac.Write([]byte(parts[0]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	return hmac.Equal(mac.Sum(nil), sig)
}

// AuthMiddleware 登录校验中间件：
// 页面未登录跳转 /login；API 未登录返回 401；/login、/health、/ready、/static 放行。
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authEnabled() {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		if p == "/login" || p == "/health" || p == "/ready" || strings.HasPrefix(p, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(authCookieName); err == nil && verifyToken(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "未登录或会话已过期"}`))
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

// LoginPage 登录页（独立页面，不走侧边栏 layout）
func LoginPage(w http.ResponseWriter, r *http.Request) {
	if !authEnabled() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	errHint := ""
	if r.URL.Query().Get("error") == "1" {
		errHint = `<p class="text-sm text-red-600 mb-3">密码错误，请重试</p>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>登录 - RSS AI Reader</title>
<script src="/static/js/tailwind.min.js"></script>
</head>
<body class="bg-[#f4f3f1] min-h-screen flex items-center justify-center">
<div class="bg-white rounded-xl shadow-sm border border-[#e8e6e2] p-8 w-full max-w-sm">
<h1 class="text-2xl font-bold text-gray-900 mb-1" style="font-family: Georgia, 'Times New Roman', serif;">RSS AI Reader</h1>
<p class="text-sm text-gray-400 mb-6">请输入访问密码</p>
<form method="post" action="/login">
` + errHint + `
<input type="password" name="password" required autofocus
       class="w-full px-3 py-2 border border-gray-300 rounded-lg mb-4 text-sm focus:ring-2 focus:ring-[#1a6b3c] focus:border-[#1a6b3c] outline-none"
       placeholder="密码">
<button type="submit"
        class="w-full bg-[#1a6b3c] hover:bg-[#175a33] text-white py-2 rounded-lg text-sm font-medium transition">
登 录
</button>
</form>
</div>
</body>
</html>`))
}

// LoginSubmit 处理登录表单提交
func LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !authEnabled() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	pwd := r.PostFormValue("password")
	if subtle.ConstantTimeCompare([]byte(pwd), []byte(appConfig.Server.Password)) != 1 {
		time.Sleep(authLoginFailDelay)
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    signToken(time.Now()),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(authSessionMaxAge.Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// Logout 退出登录，清除会话
func Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}
