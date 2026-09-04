package processor

import (
	"fmt"
	"regexp"
	"rss-ai/internal/models"
	"strings"
)

// htmlTagRegex 匹配 HTML 标签的正则表达式
var htmlTagRegex = regexp.MustCompile(`<[^>]+>`)

// getArticleContentForAI 获取文章内容用于 AI 处理
// 优先级: AISummary > ContentCleaned > Summary > Content
func getArticleContentForAI(a *models.Article) string {
	// 1. 优先使用 AI 摘要
	if a.AISummary != "" {
		return a.AISummary
	}
	// 2. 使用清理后的内容
	if a.ContentCleaned != "" {
		content := a.ContentCleaned
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		return content
	}
	// 3. 使用原始摘要（清理 HTML）
	if a.Summary != "" {
		return strings.TrimSpace(htmlTagRegex.ReplaceAllString(a.Summary, ""))
	}
	// 4. 使用原始内容（清理 HTML）
	if a.Content != "" {
		content := a.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		return strings.TrimSpace(htmlTagRegex.ReplaceAllString(content, ""))
	}
	return ""
}

// 通用提示词模板（动态注入 LLM 分组信息，JSON 输出）
const (
	// UniversalFeaturedPrompt 通用重点报道模板（JSON 输出）
	UniversalFeaturedPrompt = `你是资深新闻编辑，专注于{{domain}}领域的报道。

## 主题
{{topic_name}}
相关标签: {{tags}}

## 素材（多篇相关报道）
{{materials}}

## 任务
将以上多篇报道综合成一篇 300-500 字的深度报道。

## 要求
1. **综合多源信息**：将多篇报道的核心内容融合，不要逐篇罗列
2. **去除冗余**：相同信息只说一次，不同信息互相补充
3. **客观报道**：引用关键数据和观点，标注来源（如"据彭博社报道"）
4. **提炼要点**：总结 3 个关键要点
5. **洞察预测**：末尾加 1-2 句行业影响分析

## 输出格式（必须严格输出以下 JSON 格式）
{
  "content": "综合报道正文（300-500字，不要重复主题标题）",
  "key_points": ["要点1", "要点2", "要点3"],
  "insight": "洞察预测（1-2句）"
}

注意：只输出 JSON，不要包含任何其他文字或 markdown 代码块标记。`

	// UniversalBriefPrompt 通用简讯模板（JSON 输出）
	UniversalBriefPrompt = `你是资深新闻编辑。

## 主题
{{topic_name}}

## 素材
{{materials}}

## 任务
基于以上素材，撰写一条 100 字以内的精简报道。

## 要求
1. 概括核心事实
2. 末尾加一句简短洞察

## 输出格式（必须严格输出以下 JSON 格式）
{
  "title": "简讯标题",
  "content": "简讯正文（100字以内）",
  "insight": "简短洞察",
  "source": "来源名称"
}

注意：只输出 JSON，不要包含任何其他文字或 markdown 代码块标记。`
)

// TopicMatcher 主题匹配器（简化版，主要提供分组信息）
type TopicMatcher struct{}

// NewTopicMatcher 创建主题匹配器
func NewTopicMatcher() *TopicMatcher {
	return &TopicMatcher{}
}

// GetPromptForCluster 为聚类获取提示词（直接使用 LLM 分组信息）
func (m *TopicMatcher) GetPromptForCluster(cluster *models.ArticleCluster) *models.TopicPrompt {
	return &models.TopicPrompt{
		ID:       0,
		Name:     cluster.Name,                 // LLM 分组名称
		Persona:  buildPersona(cluster.Domain), // 根据领域生成角色
		Keywords: strings.Join(cluster.RepresentativeTags, ","),
	}
}

// buildPersona 根据领域构建角色描述
func buildPersona(domain string) string {
	personaMap := map[string]string{
		"科技": "资深科技编辑，关注技术突破和行业影响",
		"医疗": "医疗健康编辑，关注医学进展和健康影响",
		"财经": "财经分析师，关注市场动态和经济影响",
		"体育": "体育新闻编辑，关注赛事动态和运动员表现",
		"娱乐": "娱乐新闻编辑，关注影视动态和明星资讯",
		"时事": "时事观察专家，关注政策变化和社会影响",
		"教育": "教育领域编辑，关注教育政策和学习资源",
		"汽车": "汽车行业编辑，关注新车发布和行业趋势",
		"房产": "房产资讯编辑，关注楼市动态和政策变化",
	}

	if persona, ok := personaMap[domain]; ok {
		return persona
	}
	return "资深新闻编辑"
}

// PromptBuilder 提示词构建器
type PromptBuilder struct{}

// NewPromptBuilder 创建提示词构建器
func NewPromptBuilder() *PromptBuilder {
	return &PromptBuilder{}
}

// BuildFeaturedPrompt 构建重点报道提示词（动态注入 LLM 分组信息）
func (b *PromptBuilder) BuildFeaturedPrompt(cluster *models.ArticleCluster, _ *models.TopicPrompt) string {
	materials := b.buildMaterials(cluster)

	// 动态填充模板
	result := UniversalFeaturedPrompt
	result = strings.ReplaceAll(result, "{{domain}}", cluster.Domain)
	result = strings.ReplaceAll(result, "{{topic_name}}", cluster.Name)
	result = strings.ReplaceAll(result, "{{tags}}", strings.Join(cluster.RepresentativeTags, ", "))
	result = strings.ReplaceAll(result, "{{materials}}", materials)

	return result
}

// BuildBriefPrompt 构建简讯提示词（动态注入 LLM 分组信息）
func (b *PromptBuilder) BuildBriefPrompt(cluster *models.ArticleCluster, _ *models.TopicPrompt) string {
	materials := b.buildMaterials(cluster)

	// 动态填充模板
	result := UniversalBriefPrompt
	result = strings.ReplaceAll(result, "{{topic_name}}", cluster.Name)
	result = strings.ReplaceAll(result, "{{materials}}", materials)

	return result
}

// buildMaterials 构建素材内容（不包含链接，减少 token 消耗）
func (b *PromptBuilder) buildMaterials(cluster *models.ArticleCluster) string {
	var sb strings.Builder

	for i, a := range cluster.Articles {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, "**文章 %d (ID:%d): %s**\n", i+1, a.ID, a.Title)
		// 使用统一的内容获取函数，优先级: AISummary > ContentCleaned > Summary > Content
		content := getArticleContentForAI(a)
		if content != "" {
			fmt.Fprintf(&sb, "摘要: %s\n", content)
		}
	}

	return sb.String()
}

// FeaturedReportJSON 重点报道 JSON 结构
type FeaturedReportJSON struct {
	Content   string   `json:"content"`
	KeyPoints []string `json:"key_points"`
	Insight   string   `json:"insight"`
}

// BriefReportJSON 简讯 JSON 结构
type BriefReportJSON struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Insight string `json:"insight"`
	Source  string `json:"source"`
}

// FormatFeaturedReport 格式化重点报道（JSON → Markdown）
func FormatFeaturedReport(report *FeaturedReportJSON, articles []*models.Article) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "%s\n\n", report.Content)

	if len(report.KeyPoints) > 0 {
		sb.WriteString("**要点：**\n\n")
		for _, p := range report.KeyPoints {
			fmt.Fprintf(&sb, "- %s\n", p)
		}
		sb.WriteString("\n")
	}

	if report.Insight != "" {
		fmt.Fprintf(&sb, "**洞察：** %s\n\n", report.Insight)
	}

	if len(articles) > 0 {
		sb.WriteString("**来源:** ")
		for i, a := range articles {
			if i > 0 {
				sb.WriteString(" | ")
			}
			// 使用序号作为显示文本，完整标题放在 title 属性中
			// Markdown 链接格式: [显示文本](URL "title")
			fmt.Fprintf(&sb, "[[%d]](%s \"%s\")", i+1, a.Link, strings.ReplaceAll(a.Title, "\"", "'"))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// FormatBriefReport 格式化简讯（JSON → Markdown）

// FormatBriefReport 格式化简讯（JSON → Markdown）
func FormatBriefReport(report *BriefReportJSON, articles []*models.Article) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "- **%s** %s", report.Title, report.Content)
	if report.Insight != "" {
		fmt.Fprintf(&sb, "\n **洞察：** %s", report.Insight)
	}
	if len(articles) > 0 {
		sb.WriteString("\n **来源：** ")
		for i, a := range articles {
			if i > 0 {
				sb.WriteString(" ")
			}
			fmt.Fprintf(&sb, "[[%d]](%s \"%s\")", i+1, a.Link, strings.ReplaceAll(a.Title, "\"", "'"))
		}
	}
	sb.WriteString("\n")

	return sb.String()
}
