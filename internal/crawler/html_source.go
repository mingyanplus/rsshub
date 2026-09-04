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

// HtmlSourceConfig HTML 源配置
type HtmlSourceConfig struct {
	URL             string `json:"url"`
	ItemSelector    string `json:"item_selector"`
	TitleSelector   string `json:"title_selector"`
	LinkSelector    string `json:"link_selector"`
	LinkAttr        string `json:"link_attr"`
	DateSelector    string `json:"date_selector"`
	ContentSelector string `json:"content_selector"`
	BaseURL         string `json:"base_url"`
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
	data, err := FetchFeed(ctx, s.config.URL)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	feed := &Feed{
		Title: doc.Find("title").First().Text(),
		Link:  s.config.URL,
	}

	doc.Find(s.config.ItemSelector).Each(func(i int, sel *goquery.Selection) {
		item := &FeedItem{}

		if s.config.TitleSelector != "" {
			item.Title = strings.TrimSpace(sel.Find(s.config.TitleSelector).First().Text())
		}

		if s.config.LinkSelector != "" {
			linkSel := sel.Find(s.config.LinkSelector).First()
			link, exists := linkSel.Attr(s.config.LinkAttr)
			if exists {
				item.Link = resolveURL(s.config.BaseURL, link)
			}
		}

		if s.config.DateSelector != "" {
			dateSel := sel.Find(s.config.DateSelector).First()
			dateStr := dateSel.AttrOr("datetime", "")
			if dateStr == "" {
				dateStr = strings.TrimSpace(dateSel.Text())
			}
			if t, err := parseDate(dateStr); err == nil {
				item.PublishedParsed = &t
				item.Published = t.Format(time.RFC3339)
			}
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
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date: %s", s)
}
