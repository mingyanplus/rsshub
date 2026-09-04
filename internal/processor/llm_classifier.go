package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"rss-ai/internal/ai"
	"rss-ai/internal/models"
	"strings"
	"time"
)

// LLMClassifier LLM 文章分类器
type LLMClassifier struct {
	analyzer *ai.Analyzer
	timeout  time.Duration
}

// NewLLMClassifier 创建 LLM 分类器
func NewLLMClassifier(analyzer *ai.Analyzer) *LLMClassifier {
	return &LLMClassifier{
		analyzer: analyzer,
		timeout:  120 * time.Second,
	}
}

// ArticleForClassify 待分类文章信息
type ArticleForClassify struct {
	ID    int64    `json:"id"`
	Title string   `json:"title,omitempty"`
	Tags  []string `json:"tags"`
}

// ClassifyResult LLM 分类结果
type ClassifyResult struct {
	Groups []ArticleGroup `json:"groups"`
}

// ArticleGroup 文章分组
type ArticleGroup struct {
	Name               string   `json:"name"`                // 分组名称，如"芯片半导体"
	Domain             string   `json:"domain"`              // 领域，如"科技"、"医疗"
	ArticleIDs         []int64  `json:"article_ids"`         // 分组内的文章 ID
	RepresentativeTags []string `json:"representative_tags"` // 代表性标签
}

// ClassifyArticles 对文章进行 LLM 语义分组
func (c *LLMClassifier) ClassifyArticles(ctx context.Context, articles []*models.Article) (*ClassifyResult, error) {
	if len(articles) == 0 {
		return &ClassifyResult{}, nil
	}

	// 准备分类请求数据
	classifyArticles := c.prepareArticlesForClassify(articles)

	// 构建 prompt
	prompt := c.buildClassifyPrompt(classifyArticles)

	// 调用 LLM
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	response, err := c.analyzer.Chat(ctx, prompt)
	if err != nil {
		log.Printf("LLM 分类失败: %v，使用降级方案", err)
		return c.fallbackClassify(articles), nil
	}

	// 解析结果
	result, err := c.parseClassifyResponse(response, articles)
	if err != nil {
		log.Printf("解析 LLM 分类结果失败: %v，使用降级方案", err)
		return c.fallbackClassify(articles), nil
	}

	log.Printf("LLM 分类完成，共 %d 个分组", len(result.Groups))
	return result, nil
}

// prepareArticlesForClassify 准备待分类文章数据
func (c *LLMClassifier) prepareArticlesForClassify(articles []*models.Article) []ArticleForClassify {
	result := make([]ArticleForClassify, 0, len(articles))

	for _, a := range articles {
		// 优先使用 tags，没有 tags 则使用 keywords
		tags := parseTags(a.TagsCache)
		if len(tags) == 0 {
			tags = parseKeywords(a.Keywords)
		}

		// 如果都没有，跳过（后续会作为单独文章处理）
		if len(tags) == 0 {
			continue
		}

		// 限制标签数量，避免 prompt 过长
		if len(tags) > 10 {
			tags = tags[:10]
		}

		result = append(result, ArticleForClassify{
			ID:    a.ID,
			Title: a.Title,
			Tags:  tags,
		})
	}

	return result
}

// buildClassifyPrompt 构建分类 prompt
func (c *LLMClassifier) buildClassifyPrompt(articles []ArticleForClassify) string {
	var sb strings.Builder

	sb.WriteString(`你是专业的新闻编辑。请根据以下文章的标签信息，将相似主题的文章归为一组。

## 分类要求
1. 同一篇文章只能归入一个组
2. 根据标签的语义关联分组（注意："苹果+CPU"与"苹果+水果"是不同主题）
3. 为每个组命名：**必须是具体的事件/话题描述，而非泛泛的类别名**
4. 合并过于细碎的分组（文章数少于2个的组尽量合并到相关组）
5. 提取每个组的代表性标签

## 命名示例
❌ 错误：英伟达、芯片半导体、AI（太泛泛）
✅ 正确：英伟达重启 H200 芯片供应中国、OpenAI 发布 GPT-5、特斯拉 FSD 入华获批

## 文章列表
`)

	// 添加文章列表
	for i, a := range articles {
		fmt.Fprintf(&sb, "%d. ID:%d 标签:[%s]\n", i+1, a.ID, strings.Join(a.Tags, ", "))
	}

	sb.WriteString(`
## 输出格式
请返回纯 JSON 格式（不要 markdown 代码块）：
{
  "groups": [
    {
      "name": "分组名称（具体事件描述，如'英伟达重启 H200 芯片供应中国'）",
      "domain": "领域（科技/医疗/财经/体育/娱乐/时事/生活等）",
      "article_ids": [文章ID列表],
      "representative_tags": ["代表性标签1", "标签2"]
    }
  ]
}

现在请开始分类：`)

	return sb.String()
}

// parseClassifyResponse 解析 LLM 分类响应
func (c *LLMClassifier) parseClassifyResponse(response string, articles []*models.Article) (*ClassifyResult, error) {
	// 使用公共函数提取 JSON
	response = ai.ExtractJSONFromResponse(response)

	var result ClassifyResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}

	// 验证并修复结果
	c.validateAndFixResult(&result, articles)

	return &result, nil
}

// validateAndFixResult 验证并修复分类结果
func (c *LLMClassifier) validateAndFixResult(result *ClassifyResult, articles []*models.Article) {
	// 构建文章 ID 集合
	articleSet := make(map[int64]bool)
	for _, a := range articles {
		articleSet[a.ID] = true
	}

	// 记录已分配的文章
	assigned := make(map[int64]bool)
	validGroups := make([]ArticleGroup, 0)

	for _, group := range result.Groups {
		// 过滤无效的文章 ID
		validIDs := make([]int64, 0)
		for _, id := range group.ArticleIDs {
			if articleSet[id] && !assigned[id] {
				validIDs = append(validIDs, id)
				assigned[id] = true
			}
		}

		// 只保留有效的分组
		if len(validIDs) > 0 {
			group.ArticleIDs = validIDs
			if group.Name == "" {
				group.Name = "未命名分组"
			}
			if group.Domain == "" {
				group.Domain = "通用"
			}
			validGroups = append(validGroups, group)
		}
	}

	result.Groups = validGroups

	// 处理未分配的文章
	var unassignedIDs []int64
	for id := range articleSet {
		if !assigned[id] {
			unassignedIDs = append(unassignedIDs, id)
		}
	}

	// 将未分配的文章各自成组
	for _, id := range unassignedIDs {
		result.Groups = append(result.Groups, ArticleGroup{
			Name:       "其他资讯",
			Domain:     "通用",
			ArticleIDs: []int64{id},
		})
	}
}

// fallbackClassify 降级分类方案（当 LLM 失败时使用）
func (c *LLMClassifier) fallbackClassify(articles []*models.Article) *ClassifyResult {
	// 使用简单的 tag 分组作为降级方案
	groupMap := make(map[string]*ArticleGroup)

	for _, a := range articles {
		tags := parseTags(a.TagsCache)
		if len(tags) == 0 {
			tags = parseKeywords(a.Keywords)
		}

		if len(tags) == 0 {
			// 无标签的文章归入"其他"
			if _, exists := groupMap["其他"]; !exists {
				groupMap["其他"] = &ArticleGroup{
					Name:       "其他资讯",
					Domain:     "通用",
					ArticleIDs: []int64{},
				}
			}
			groupMap["其他"].ArticleIDs = append(groupMap["其他"].ArticleIDs, a.ID)
			continue
		}

		// 使用第一个标签作为分组依据
		primaryTag := tags[0]
		if _, exists := groupMap[primaryTag]; !exists {
			groupMap[primaryTag] = &ArticleGroup{
				Name:               primaryTag,
				Domain:             "通用",
				ArticleIDs:         []int64{},
				RepresentativeTags: []string{primaryTag},
			}
		}
		groupMap[primaryTag].ArticleIDs = append(groupMap[primaryTag].ArticleIDs, a.ID)
	}

	// 转换为结果
	result := &ClassifyResult{
		Groups: make([]ArticleGroup, 0, len(groupMap)),
	}
	for _, group := range groupMap {
		result.Groups = append(result.Groups, *group)
	}

	return result
}
