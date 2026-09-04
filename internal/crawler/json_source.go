package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

// JsonSourceConfig JSON API 源配置
type JsonSourceConfig struct {
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	Headers      map[string]string `json:"headers"`
	Body         string            `json:"body"`
	ItemsPath    string            `json:"items_path"`
	TitleField   string            `json:"title_field"`
	LinkField    string            `json:"link_field"`
	DateField    string            `json:"date_field"`
	ContentField string            `json:"content_field"`
}

// JsonSource JSON API 数据源
type JsonSource struct {
	config JsonSourceConfig
}

// NewJsonSource 创建 JSON 源
func NewJsonSource(feedURL, configJSON string) (*JsonSource, error) {
	var cfg JsonSourceConfig
	if err := parseSourceConfig(configJSON, &cfg); err != nil {
		return nil, fmt.Errorf("invalid json source config: %w", err)
	}
	if cfg.URL == "" {
		cfg.URL = feedURL
	}
	if cfg.Method == "" {
		cfg.Method = "GET"
	}
	if cfg.ItemsPath == "" {
		return nil, fmt.Errorf("items_path is required for json source")
	}
	return &JsonSource{config: cfg}, nil
}

func (s *JsonSource) FetchAndParse(ctx context.Context) (*Feed, error) {
	var body io.Reader
	if s.config.Body != "" {
		body = strings.NewReader(s.config.Body)
	}

	req, err := http.NewRequestWithContext(ctx, s.config.Method, s.config.URL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "RSS-AI-Reader/1.0")
	for k, v := range s.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var jsonData interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	itemsRaw, err := jqQuery(jsonData, s.config.ItemsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to query items_path '%s': %w", s.config.ItemsPath, err)
	}

	itemsArr, ok := itemsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("items_path '%s' did not return an array", s.config.ItemsPath)
	}

	feed := &Feed{
		Title: "JSON Source",
		Link:  s.config.URL,
	}

	for _, itemRaw := range itemsArr {
		item := &FeedItem{}

		if s.config.TitleField != "" {
			if v, err := jqQuery(itemRaw, s.config.TitleField); err == nil {
				item.Title = fmt.Sprint(v)
			}
		}
		if s.config.LinkField != "" {
			if v, err := jqQuery(itemRaw, s.config.LinkField); err == nil {
				item.Link = fmt.Sprint(v)
			}
		}
		if s.config.DateField != "" {
			if v, err := jqQuery(itemRaw, s.config.DateField); err == nil {
				if t, err := parseDate(fmt.Sprint(v)); err == nil {
					item.PublishedParsed = &t
					item.Published = t.Format(time.RFC3339)
				}
			}
		}
		if s.config.ContentField != "" {
			if v, err := jqQuery(itemRaw, s.config.ContentField); err == nil {
				item.Content = fmt.Sprint(v)
			}
		}

		if item.Title != "" || item.Link != "" {
			feed.Items = append(feed.Items, item)
		}
	}

	log.Printf("JSON source: extracted %d items from %s", len(feed.Items), s.config.URL)
	return feed, nil
}

// jqQuery 使用 gojq 执行查询
func jqQuery(data interface{}, query string) (interface{}, error) {
	q, err := gojq.Parse(query)
	if err != nil {
		return nil, err
	}
	var result interface{}
	iter := q.Run(data)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			return nil, err
		}
		result = v
	}
	return result, nil
}
