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

// readerEnabled 是否启用阅读密码（server.reader_password 留空则只有管理员一级）
func readerEnabled() bool {
	return appConfig != nil && appConfig.Server.ReaderPassword != ""
}

// sessionKey 会话签名密钥由两个密码共同派生：任一密码修改后旧会话全部自动失效
func sessionKey() []byte {
	sum := sha256.Sum256([]byte("rss-ai:" + appConfig.Server.Password + "|" + appConfig.Server.ReaderPassword))
	return sum[:]
}

// signToken 生成 "级别.过期时间戳.签名" 形式的会话令牌（级别 a=管理员 / r=访客）
func signToken(now time.Time, isAdmin bool) string {
	lvl := "r"
	if isAdmin {
		lvl = "a"
	}
	expiry := strconv.FormatInt(now.Add(authSessionMaxAge).Unix(), 10)
	msg := lvl + "." + expiry
	mac := hmac.New(sha256.New, sessionKey())
	mac.Write([]byte(msg))
	return msg + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyToken 校验会话令牌，返回是否有效与会话级别（管理员/访客）
func verifyToken(token string) (valid, isAdmin bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false, false
	}
	switch parts[0] {
	case "a":
		isAdmin = true
	case "r":
		isAdmin = false
	default:
		return false, false
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return false, false
	}
	mac := hmac.New(sha256.New, sessionKey())
	mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false, false
	}
	if !hmac.Equal(mac.Sum(nil), sig) {
		return false, false
	}
	return true, isAdmin
}

// 会话权限级别：authLevel 的返回值
const (
	authLevelNone   = 0 // 未登录
	authLevelReader = 1 // 访客（阅读密码登录，只读）
	authLevelAdmin  = 2 // 管理员（未启用登录时全员视为管理员）
)

// authLevel 解析请求的会话级别，见上方常量
func authLevel(r *http.Request) int {
	if !authEnabled() {
		return authLevelAdmin
	}
	c, err := r.Cookie(authCookieName)
	if err != nil {
		return authLevelNone
	}
	valid, isAdmin := verifyToken(c.Value)
	switch {
	case !valid:
		return authLevelNone
	case isAdmin:
		return authLevelAdmin
	default:
		return authLevelReader
	}
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
		if authLevel(r) > authLevelNone {
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

// AdminMiddleware 管理员校验中间件：访客（阅读密码登录）访问管理页面跳回首页、
// 调用管理 API 返回 403。未启用登录时全员视为管理员，直接放行
func AdminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authLevel(r) == authLevelAdmin {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error": "需要管理员权限，请用管理员密码重新登录"}`))
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
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
	readerHint := ""
	if readerEnabled() {
		readerHint = `<p class="text-xs text-gray-400 mt-3">输入阅读密码将以访客身份进入（仅浏览，不可管理）</p>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>登录 - AI Reader</title>
<script src="/static/js/tailwind.min.js"></script>
</head>
<body class="bg-[#f4f3f1] min-h-screen flex items-center justify-center">
<div class="bg-white rounded-xl shadow-sm border border-[#e8e6e2] p-8 w-full max-w-sm">
<h1 class="text-2xl font-bold text-gray-900 mb-1" style="font-family: Georgia, 'Times New Roman', serif;">AI Reader</h1>
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
` + readerHint + `
</div>
</body>
</html>`))
}

// LoginSubmit 处理登录表单提交：密码匹配管理员密码进管理态，
// 否则匹配阅读密码进访客态（只读）。两个密码都做恒时比较后再分支，避免时序侧信道
func LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !authEnabled() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	pwd := r.PostFormValue("password")
	adminOK := subtle.ConstantTimeCompare([]byte(pwd), []byte(appConfig.Server.Password)) == 1
	readerOK := readerEnabled() && subtle.ConstantTimeCompare([]byte(pwd), []byte(appConfig.Server.ReaderPassword)) == 1

	var isAdmin bool
	switch {
	case adminOK:
		isAdmin = true
	case readerOK:
		isAdmin = false
	default:
		time.Sleep(authLoginFailDelay)
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    signToken(time.Now(), isAdmin),
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
		Name:   authCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}
