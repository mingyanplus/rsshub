package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveHTML 起一个返回固定 UTF-8 HTML 的本地服务
func serveHTML(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return serveBytes(t, []byte(body), "text/html; charset=utf-8")
}

// serveBytes 起一个返回指定字节流与 Content-Type 的本地服务（模拟非 UTF-8 老站）
func serveBytes(t *testing.T, body []byte, contentType string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Write(body)
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

// GB2312 站点：响应头声明编码，列表页自动转码不乱码
func TestHtmlSourceGB2312AutoDecode(t *testing.T) {
	page := `<html><head><title>考试院</title></head><body><ul class="list">
		<li><a href="/n1.html">2026年普通高校招生工作的通知</a></li>
	</ul></body></html>`
	srv := serveBytes(t, encodeGB18030(t, page), "text/html; charset=gb2312")

	config := `{"url":"` + srv.URL + `","item_selector":".list a"}`
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
	if feed.Items[0].Title != "2026年普通高校招生工作的通知" {
		t.Errorf("Items[0].Title = %v, want GB2312 自动转码后的中文标题", feed.Items[0].Title)
	}
}

// 无任何编码声明的 GBK 站点：自动识别失效，配置 encoding 手动兜底
func TestHtmlSourceManualEncoding(t *testing.T) {
	page := `<html><head><title>无声明</title></head><body><ul class="list">
		<li><a href="/n2.html">关于调整考试时间的公告</a></li>
	</ul></body></html>`
	// 无 meta 无 header charset，模拟识别不出的老站
	srv := serveBytes(t, encodeGB18030(t, page), "text/html")

	config := `{"url":"` + srv.URL + `","item_selector":".list a","encoding":"gbk"}`
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
	if feed.Items[0].Title != "关于调整考试时间的公告" {
		t.Errorf("Items[0].Title = %v, want 手动 gbk 转码后的中文标题", feed.Items[0].Title)
	}
}

// 「选择器@属性」约定语法：链接/标题/日期均可从任意属性取值。
// 页面模拟非标准结构：链接在条目自身 data-url、标题在 img 的 alt、日期在 data-time
func TestHtmlSourceSelectorAttrSyntax(t *testing.T) {
	srv := serveHTML(t, `<!DOCTYPE html>
<html><head><title>自定义属性站点</title></head>
<body>
<ul class="list">
	<li class="item" data-url="/detail/1.html">
		<img src="/img1.png" alt="通过图片alt取到的标题">
		<span class="pub" data-time="2026-09-01">昨天</span>
	</li>
</ul>
</body></html>`)

	// 标题选择器 img@alt、链接选择器留空 + 条目自身 @data-url、日期 .pub@data-time
	config := `{"url":"` + srv.URL + `","item_selector":".item","title_selector":"img@alt","link_selector":"@data-url","date_selector":".pub@data-time"}`
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
	it := feed.Items[0]
	if it.Title != "通过图片alt取到的标题" {
		t.Errorf("Title = %v, want img@alt 取值", it.Title)
	}
	if it.Link != srv.URL+"/detail/1.html" {
		t.Errorf("Link = %v, want 条目自身 @data-url 取值并补全", it.Link)
	}
	if it.PublishedParsed == nil || it.PublishedParsed.Format("2006-01-02") != "2026-09-01" {
		t.Errorf("PublishedParsed = %v, want .pub@data-time 取 2026-09-01", it.PublishedParsed)
	}
}

// @ 语法取子元素属性（a@data-url）；@ 属性不存在时回退默认行为
func TestHtmlSourceSelectorAttrOnChildAndFallback(t *testing.T) {
	srv := serveHTML(t, `<!DOCTYPE html>
<html><head><title>回退</title></head>
<body>
<ul class="list">
	<li class="item"><a href="/ok.html" data-url="/real.html">链接文本不是标题</a></li>
	<li class="item"><a href="/plain.html">纯链接条目</a></li>
</ul>
</body></html>`)

	// link_selector 用 a@data-url：第一条命中属性，第二条无该属性应回退默认 href
	config := `{"url":"` + srv.URL + `","item_selector":".item","link_selector":"a@data-url"}`
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
	if feed.Items[0].Link != srv.URL+"/real.html" {
		t.Errorf("Items[0].Link = %v, want a@data-url 取值", feed.Items[0].Link)
	}
	if feed.Items[1].Link != srv.URL+"/plain.html" {
		t.Errorf("Items[1].Link = %v, want 属性缺失回退默认 href", feed.Items[1].Link)
	}
}
