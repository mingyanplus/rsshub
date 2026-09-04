package crawler

import (
	"context"
	"encoding/json"
	"fmt"
)

// Source 数据源接口
type Source interface {
	FetchAndParse(ctx context.Context) (*Feed, error)
}

// NewSource 根据 source_type 创建对应 Source
func NewSource(url, sourceType, sourceConfigJSON string) (Source, error) {
	switch sourceType {
	case "rss", "":
		return &RssSource{url: url}, nil
	case "html":
		return NewHtmlSource(url, sourceConfigJSON)
	case "json":
		return NewJsonSource(url, sourceConfigJSON)
	default:
		return nil, fmt.Errorf("unsupported source_type: %s", sourceType)
	}
}

// RssSource RSS/Atom/JSON Feed 源（包装现有 FetchAndParse）
type RssSource struct {
	url string
}

func (s *RssSource) FetchAndParse(ctx context.Context) (*Feed, error) {
	return FetchAndParse(ctx, s.url)
}

// parseSourceConfig 解析 JSON 配置到目标结构体
func parseSourceConfig(jsonStr string, target interface{}) error {
	if jsonStr == "" || jsonStr == "{}" {
		return fmt.Errorf("source_config is empty")
	}
	return json.Unmarshal([]byte(jsonStr), target)
}
