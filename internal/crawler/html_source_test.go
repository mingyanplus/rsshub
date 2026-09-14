package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveHTML 起一个返回固定 HTML 的本地服务
func serveHTML(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// cqksy 风格：条目本身是 <a>，标题/日期在子元素里，链接选择器留空
func TestHtmlSourceSelfLinkFallback(t *testing.T) {
	srv := serveHTML(t, `<!DOCTYPE html>
<html><head><title>重庆市教育考试院</title></head>
<body>
<div class="main-content wrapper">
	<a href="https://www.cqksy.cn/web/article/2026-08/09/content_7104.html" target="_blank" class="content-item">
		<img src="https://www.cqksy.cn/ksyres/img/jiantou.png" alt="">
		<p>2026年普通高校招生第七阶段（高职专科批）第二次征集志愿公告</p>
		<span>2026-08-09</span>
	</a>
	<a href="/uploadFile/infopub/202608/2086392087385219072.pdf" target="_blank" class="content-item">
		<img src="https://www.cqksy.cn/ksyres/img/jiantou.png" alt="">
		<p>2026年重庆市普通高校招生信息表高职专科批-历史-平行志愿-第2次征集</p>
		<span>2026-08-09</span>
	</a>
</div>
</body></html>`)

	config := `{"url":"` + srv.URL + `","item_selector":"a.content-item","title_selector":"p","date_selector":"span"}`
	src, err := NewHtmlSource("", config)
	if err != nil {
		t.Fatalf("NewHtmlSource() error = %v", err)
	}

	feed, err := src.FetchAndParse(context.Background())
	if err != nil {
		t.Fatalf("FetchAndParse() error = %v", err)
	}

	if len(feed.Items) != 2 {
		t.Fatalf("len(Items) = %v, want 2", len(feed.Items))
	}

	// 链接选择器留空 → 回退取条目自身 href（绝对链接）
	if feed.Items[0].Link != "https://www.cqksy.cn/web/article/2026-08/09/content_7104.html" {
		t.Errorf("Items[0].Link = %v, want 自身 href", feed.Items[0].Link)
	}
	// 相对链接用页面 URL 补全
	if feed.Items[1].Link != srv.URL+"/uploadFile/infopub/202608/2086392087385219072.pdf" {
		t.Errorf("Items[1].Link = %v, want 相对链接补全为绝对链接", feed.Items[1].Link)
	}
	if feed.Items[0].Title != "2026年普通高校招生第七阶段（高职专科批）第二次征集志愿公告" {
		t.Errorf("Items[0].Title = %v, want p 元素文本", feed.Items[0].Title)
	}
	if feed.Items[0].PublishedParsed == nil || feed.Items[0].PublishedParsed.Format("2006-01-02") != "2026-08-09" {
		t.Errorf("Items[0].PublishedParsed = %v, want 2026-08-09", feed.Items[0].PublishedParsed)
	}
}

// 纯链接列表：条目就是 <a>标题</a>，所有选择器留空 → 标题/链接均回退自身
func TestHtmlSourcePureLinkItems(t *testing.T) {
	srv := serveHTML(t, `<!DOCTYPE html>
<html><head><title>通知列表</title></head>
<body>
<ul class="news-list">
	<li><a href="/notice/1.html">关于做好2026年报名工作的通知</a></li>
	<li><a href="/notice/2.html">关于调整考试时间的公告</a></li>
</ul>
</body></html>`)

	// item_selector 直接锚到 <a>，其余全部留空
	config := `{"url":"` + srv.URL + `","item_selector":".news-list a"}`
	src, err := NewHtmlSource("", config)
	if err != nil {
		t.Fatalf("NewHtmlSource() error = %v", err)
	}

	feed, err := src.FetchAndParse(context.Background())
	if err != nil {
		t.Fatalf("FetchAndParse() error = %v", err)
	}

	if len(feed.Items) != 2 {
		t.Fatalf("len(Items) = %v, want 2", len(feed.Items))
	}
	if feed.Items[0].Title != "关于做好2026年报名工作的通知" {
		t.Errorf("Items[0].Title = %v, want 条目自身文本", feed.Items[0].Title)
	}
	if feed.Items[0].Link != srv.URL+"/notice/1.html" {
		t.Errorf("Items[0].Link = %v, want 自身 href 补全", feed.Items[0].Link)
	}
}

// 传统结构：子元素选择器显式填写时行为不变（回退不覆盖显式配置）
func TestHtmlSourceDescendantSelectorsUnchanged(t *testing.T) {
	srv := serveHTML(t, `<!DOCTYPE html>
<html><head><title>新闻</title></head>
<body>
<div class="list">
	<div class="item">
		<h2><a href="https://example.com/a1">第一条新闻</a></h2>
		<span class="date">2026-09-01</span>
		<div class="summary">摘要内容</div>
	</div>
</div>
</body></html>`)

	config := `{"url":"` + srv.URL + `","item_selector":".item","title_selector":"h2 a","link_selector":"h2 a","date_selector":".date","content_selector":".summary"}`
	src, err := NewHtmlSource("", config)
	if err != nil {
		t.Fatalf("NewHtmlSource() error = %v", err)
	}

	feed, err := src.FetchAndParse(context.Background())
	if err != nil {
		t.Fatalf("FetchAndParse() error = %v", err)
	}

	if len(feed.Items) != 1 {
		t.Fatalf("len(Items) = %v, want 1", len(feed.Items))
	}
	if feed.Items[0].Title != "第一条新闻" {
		t.Errorf("Items[0].Title = %v, want 第一条新闻", feed.Items[0].Title)
	}
	if feed.Items[0].Link != "https://example.com/a1" {
		t.Errorf("Items[0].Link = %v, want https://example.com/a1", feed.Items[0].Link)
	}
	if feed.Items[0].Content != "摘要内容" {
		t.Errorf("Items[0].Content = %v, want 摘要内容", feed.Items[0].Content)
	}
}

// 日期选择器留空：条目自身的 datetime 属性优先，无属性时自身文本兜底
func TestHtmlSourceSelfDateFallback(t *testing.T) {
	srv := serveHTML(t, `<!DOCTYPE html>
<html><head><title>通知</title></head>
<body>
<div class="list">
	<a class="notice" href="/n1.html" datetime="2026-09-01">关于调整考试安排的通知</a>
	<time class="entry">2026-08-15</time>
</div>
</body></html>`)

	// item_selector 锚到条目自身，date_selector 留空触发自身回退
	config := `{"url":"` + srv.URL + `","item_selector":".notice, .entry"}`
	src, err := NewHtmlSource("", config)
	if err != nil {
		t.Fatalf("NewHtmlSource() error = %v", err)
	}

	feed, err := src.FetchAndParse(context.Background())
	if err != nil {
		t.Fatalf("FetchAndParse() error = %v", err)
	}

	if len(feed.Items) != 2 {
		t.Fatalf("len(Items) = %v, want 2", len(feed.Items))
	}
	// 条目自身的 datetime 属性
	if feed.Items[0].PublishedParsed == nil || feed.Items[0].PublishedParsed.Format("2006-01-02") != "2026-09-01" {
		t.Errorf("Items[0].PublishedParsed = %v, want 2026-09-01（自身 datetime 属性）", feed.Items[0].PublishedParsed)
	}
	// 无属性时自身文本兜底（<time> 本身是条目）
	if feed.Items[1].PublishedParsed == nil || feed.Items[1].PublishedParsed.Format("2006-01-02") != "2026-08-15" {
		t.Errorf("Items[1].PublishedParsed = %v, want 2026-08-15（自身文本兜底）", feed.Items[1].PublishedParsed)
	}
}
