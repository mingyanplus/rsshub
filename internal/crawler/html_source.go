package crawler

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// HtmlSourceConfig HTML 源配置。title/link/date 三个选择器均支持「选择器@属性」约定语法：
// 如 img@alt 取图片 alt 作标题、.item@data-url 取条目自身属性作链接、time@datetime 取时间属性。
// @ 在 CSS 元素选择器中不出现，无歧义；不写 @ 走默认取值（文本 / href / datetime 属性 → 文本），
// 写了 @ 但属性不存在时同样回退默认取值
type HtmlSourceConfig struct {
	URL             string `json:"url"`
	ItemSelector    string `json:"item_selector"`
	TitleSelector   string `json:"title_selector"`
	LinkSelector    string `json:"link_selector"`
	LinkAttr        string `json:"link_attr"` // 链接属性；留空默认 href（@ 语法优先于此字段，保留以兼容存量配置）
	DateSelector    string `json:"date_selector"`
	ContentSelector string `json:"content_selector"`
	BaseURL         string `json:"base_url"`
	Encoding        string `json:"encoding"` // 页面编码（gb2312/gbk/gb18030/big5 等）；留空自动识别，识别失败按 UTF-8
}

// HtmlSource HTML 页面数据源
type HtmlSource struct {
	config HtmlSourceConfig
}

// NewHtmlSource 创建 HTML 源
func NewHtmlSource(feedURL, configJSON string) (*HtmlSource, error) {
	var cfg HtmlSourceConfig
	if err := parseSourceConfig(configJSON, &cfg); err != nil {
		return nil, fmt.Errorf("invalid html source config: %w", err)
	}
	if cfg.URL == "" {
		cfg.URL = feedURL
	}
	if cfg.ItemSelector == "" {
		return nil, fmt.Errorf("item_selector is required for html source")
	}
	if cfg.LinkAttr == "" {
		cfg.LinkAttr = "href"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = cfg.URL
	}
	return &HtmlSource{config: cfg}, nil
}

func (s *HtmlSource) FetchAndParse(ctx context.Context) (*Feed, error) {
	// 经 FetchHTMLText 转码为 UTF-8：GB2312/GBK 等老站直接按字节当 UTF-8 解析会全页乱码
	html, err := FetchHTMLText(ctx, s.config.URL, s.config.Encoding)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	feed := &Feed{
		Title: doc.Find("title").First().Text(),
		Link:  s.config.URL,
	}

	doc.Find(s.config.ItemSelector).Each(func(i int, sel *goquery.Selection) {
		item := &FeedItem{}

		// 标题默认取文本；选择器带 @属性 时属性值优先（如 img@alt）
		titleSel, titleAttr := selectTarget(sel, s.config.TitleSelector, "")
		item.Title = strings.TrimSpace(titleSel.Text())
		if titleAttr != "" {
			if v, exists := titleSel.Attr(titleAttr); exists && strings.TrimSpace(v) != "" {
				item.Title = strings.TrimSpace(v)
			}
		}

		// 链接：@属性 优先，属性缺失或为空时回退 link_attr（默认 href）
		linkSel, linkAttrOverride := selectTarget(sel, s.config.LinkSelector, "")
		linkAttr := s.config.LinkAttr
		if linkAttrOverride != "" {
			linkAttr = linkAttrOverride
		}
		link, exists := linkSel.Attr(linkAttr)
		if (!exists || strings.TrimSpace(link) == "") && linkAttr != s.config.LinkAttr {
			link, exists = linkSel.Attr(s.config.LinkAttr)
		}
		if exists && strings.TrimSpace(link) != "" {
			item.Link = resolveURL(s.config.BaseURL, link)
		}

		// 日期取值链：@属性/datetime 属性 → 文本
		dateSel, dateAttr := selectTarget(sel, s.config.DateSelector, "datetime")
		dateStr := dateSel.AttrOr(dateAttr, "")
		if dateStr == "" {
			dateStr = strings.TrimSpace(dateSel.Text())
		}
		if t, err := parseDate(dateStr); err == nil {
			item.PublishedParsed = &t
			item.Published = t.Format(time.RFC3339)
		}

		if s.config.ContentSelector != "" {
			html, err := sel.Find(s.config.ContentSelector).First().Html()
			if err == nil {
				item.Content = strings.TrimSpace(html)
			}
		}

		if item.Title != "" || item.Link != "" {
			feed.Items = append(feed.Items, item)
		}
	})

	log.Printf("HTML source: extracted %d items from %s", len(feed.Items), s.config.URL)
	return feed, nil
}

// pickTarget 选择器非空时取条目内首个匹配子元素；留空回退条目自身
// （goquery Find 只匹配后代，条目本身就是 <a>/<time> 的列表页无法用子选择器选中自身）
func pickTarget(sel *goquery.Selection, selector string) *goquery.Selection {
	if selector == "" {
		return sel
	}
	return sel.Find(selector).First()
}

// selectTarget 解析「选择器@属性」并定位目标元素：expr 带 @ 时取其属性名，
// 未写 @ 时属性回退 defaultAttr（链接为 link_attr、日期为 datetime、标题为空=取文本）
func selectTarget(item *goquery.Selection, expr, defaultAttr string) (*goquery.Selection, string) {
	sel, attr := parseSelectorAttr(expr)
	if attr == "" {
		attr = defaultAttr
	}
	return pickTarget(item, sel), attr
}

// parseSelectorAttr 解析「选择器@属性」约定语法（img@alt、.item@data-url、@data-url 等）：
// 以最后一个 @ 分隔，@ 前为 CSS 选择器（留空表示条目自身）、后为属性名；
// 无 @ 时原样返回选择器、属性为空。@ 不出现在 CSS 元素选择器中，切分无歧义
func parseSelectorAttr(expr string) (selector, attr string) {
	if i := strings.LastIndex(expr, "@"); i >= 0 {
		return strings.TrimSpace(expr[:i]), strings.TrimSpace(expr[i+1:])
	}
	return expr, ""
}

// resolveURL 将相对链接补全为绝对链接
func resolveURL(base, ref string) string {
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return ref
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return baseURL.ResolveReference(refURL).String()
}

// parseDate 尝试解析日期字符串
// parseDate 解析日期字符串；无时区标记的格式按服务器本地时区解释
// （Go 默认按 UTC，对国内源站的墙上时间会偏差 8 小时）
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	formats := []string{
		time.RFC3339,
		"2006-01-02",
		"2006-01-02 15:04:05",
		"2006/01/02",
		"Jan 2, 2006",
		"January 2, 2006",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date: %s", s)
}
