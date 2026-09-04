package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"rss-ai/internal/proxyutil"

	"github.com/mmcdole/gofeed"
)

// HTTPClient HTTP 客户端
var HTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// SetProxy 设置内容抓取代理；proxyURL 为空时清除
func SetProxy(proxyURL string) {
	proxyutil.Apply(HTTPClient, proxyURL)
}

// ApplyContentFilter 按订阅源的过滤规则逐行清洗正文。
// rules 每行一条：默认按正则解析，正则无效时降级为纯文本包含匹配；
// 空行与 # 开头注释行忽略。匹配到的行整行剔除。
func ApplyContentFilter(content, rules string) string {
	if strings.TrimSpace(rules) == "" || content == "" {
		return content
	}
	var regexps []*regexp.Regexp
	var texts []string
	for _, line := range strings.Split(rules, "\n") {
		r := strings.TrimSpace(line)
		if r == "" || strings.HasPrefix(r, "#") {
			continue
		}
		if re, err := regexp.Compile(r); err == nil {
			regexps = append(regexps, re)
		} else {
			texts = append(texts, r)
		}
	}
	if len(regexps) == 0 && len(texts) == 0 {
		return content
	}

	kept := make([]string, 0, 64)
	for _, line := range strings.Split(content, "\n") {
		drop := false
		for _, re := range regexps {
			if re.MatchString(line) {
				drop = true
				break
			}
		}
		if !drop {
			for _, t := range texts {
				if strings.Contains(line, t) {
					drop = true
					break
				}
			}
		}
		if !drop {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// FetchFeed 从 URL 获取 RSS 内容
func FetchFeed(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "RSS-AI-Reader/1.0")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/json, text/xml, */*")

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return data, nil
}

// FetchAndParse 从 URL 获取并解析 RSS
func FetchAndParse(ctx context.Context, url string) (*Feed, error) {
	data, err := FetchFeed(ctx, url)
	if err != nil {
		return nil, err
	}

	return ParseFeed(data)
}

// Feed 表示解析后的订阅源
type Feed struct {
	Title       string     `json:"title"`
	Link        string     `json:"link"`
	Description string     `json:"description"`
	Items       []*FeedItem `json:"items"`
}

// FeedItem 表示订阅源中的一篇文章
type FeedItem struct {
	Title           string     `json:"title"`
	Link            string     `json:"link"`
	Description     string     `json:"description"`
	Content         string     `json:"content"`
	Published       string     `json:"published"`
	PublishedParsed *time.Time `json:"published_parsed,omitempty"`
}

// ParseFeed 解析 RSS/Atom/JSON Feed
func ParseFeed(data []byte) (*Feed, error) {
	// 尝试解析为 JSON Feed
	if isJSONFeed(data) {
		return parseJSONFeed(data)
	}

	// 使用 gofeed 解析 RSS/Atom
	parser := gofeed.NewParser()
	feed, err := parser.ParseString(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse feed: %w", err)
	}

	result := &Feed{
		Title:       feed.Title,
		Link:        feed.Link,
		Description: feed.Description,
		Items:       make([]*FeedItem, 0, len(feed.Items)),
	}

	for _, item := range feed.Items {
		result.Items = append(result.Items, &FeedItem{
			Title:           item.Title,
			Link:            item.Link,
			Description:     item.Description,
			Content:         item.Content,
			Published:       item.Published,
			PublishedParsed: item.PublishedParsed,
		})
	}

	return result, nil
}

// isJSONFeed 检查是否为 JSON Feed
func isJSONFeed(data []byte) bool {
	var jf struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &jf); err != nil {
		return false
	}
	return strings.HasPrefix(jf.Version, "https://jsonfeed.org")
}

// parseJSONFeed 解析 JSON Feed
func parseJSONFeed(data []byte) (*Feed, error) {
	var jf struct {
		Title       string `json:"title"`
		HomePageURL string `json:"home_page_url"`
		Items []struct {
			ID            string `json:"id"`
			Title         string `json:"title"`
			URL           string `json:"url"`
			ContentText   string `json:"content_text"`
			ContentHTML   string `json:"content_html"`
			Summary       string `json:"summary"`
			DatePublished string `json:"date_published"`
		} `json:"items"`
	}

	if err := json.Unmarshal(data, &jf); err != nil {
		return nil, fmt.Errorf("failed to parse JSON feed: %w", err)
	}

	result := &Feed{
		Title: jf.Title,
		Link: jf.HomePageURL,
		Items: make([]*FeedItem, 0, len(jf.Items)),
	}

	for _, item := range jf.Items {
		content := item.ContentHTML
		if content == "" {
			content = item.ContentText
		}

		result.Items = append(result.Items, &FeedItem{
			Title:       item.Title,
			Link:        item.URL,
			Description: item.Summary,
			Content:     content,
			Published:   item.DatePublished,
		})
	}

	return result, nil
}

// ExtractContent 从 FeedItem 中提取内容，优先返回 Content，如果为空则返回 Description
func ExtractContent(item *FeedItem) string {
	if item.Content != "" {
		return item.Content
	}
	return item.Description
}

