package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"rss-ai/internal/config"
)

// AnalyzeResult AI 分析结果
type AnalyzeResult struct {
	IsAd            bool     `json:"is_ad"`
	AdReason        string   `json:"ad_reason"`
	ContentCleaned  string   `json:"content_cleaned"`
	Summary         string   `json:"summary"`
	OneLineSummary  string   `json:"one_line_summary"` // 一句话总结（用于向量匹配）
	Keywords        []string `json:"keywords"`
	Tags            []string `json:"tags"`
	Entities        []string `json:"entities"` // 涉及的实体（公司、人物、国家等）
	ImportanceScore int      `json:"importance_score"`
	TopicCategory     string   `json:"topic_category"`
	TranslatedContent string   `json:"translated_content"` // 非中文内容的中文翻译
}

// Analyzer 文章分析器
type Analyzer struct {
	llmClient       *LLMClient
	embeddingClient *EmbeddingClient
	cache           *LLMCache    // LLM 结果缓存
	llmRateLimit    time.Duration // LLM API 调用间隔
	embRateLimit    time.Duration // Embedding API 调用间隔
	lastLLMCall     time.Time     // 上次 LLM 调用时间
	lastEmbCall     time.Time     // 上次 Embedding 调用时间

	promptMu             sync.RWMutex
	analyzeSysOverride   string // 文章分析 system 提示词覆盖（空=内置默认）
	translateSysOverride string // 翻译 system 提示词覆盖（空=内置默认）
}

// SetPromptOverrides 设置提示词覆盖（空字符串表示使用内置默认，支持热重载）
func (a *Analyzer) SetPromptOverrides(analyze, translate string) {
	a.promptMu.Lock()
	a.analyzeSysOverride = strings.TrimSpace(analyze)
	a.translateSysOverride = strings.TrimSpace(translate)
	a.promptMu.Unlock()
}

func (a *Analyzer) analyzeSystemPrompt() string {
	a.promptMu.RLock()
	defer a.promptMu.RUnlock()
	if a.analyzeSysOverride != "" {
		return a.analyzeSysOverride
	}
	return DefaultAnalyzeSystemPrompt
}

func (a *Analyzer) translateSystemPrompt() string {
	a.promptMu.RLock()
	defer a.promptMu.RUnlock()
	if a.translateSysOverride != "" {
		return a.translateSysOverride
	}
	return DefaultTranslateSystemPrompt
}

// NewAnalyzer 创建分析器
func NewAnalyzer(llmCfg *config.LLMConfig, embCfg *config.EmbeddingConfig, rateLimitCfg *config.RateLimitConfig, db *sql.DB) *Analyzer {
	a := &Analyzer{
		llmClient:       NewLLMClient(llmCfg),
		embeddingClient: NewEmbeddingClient(embCfg),
		cache:           NewLLMCache(db, 7*24*time.Hour),
	}

	// 应用速率限制配置
	if rateLimitCfg != nil {
		a.llmRateLimit = rateLimitCfg.LLMInterval
		a.embRateLimit = rateLimitCfg.EmbeddingInterval
	} else {
		// 默认值
		a.llmRateLimit = 3 * time.Second
		a.embRateLimit = 1 * time.Second
	}

	return a
}

// SetRateLimit 设置速率限制
func (a *Analyzer) SetRateLimit(llmInterval, embInterval time.Duration) {
	a.llmRateLimit = llmInterval
	a.embRateLimit = embInterval
}

// UpdateConfig 更新配置（支持热重载）
func (a *Analyzer) UpdateConfig(llmCfg *config.LLMConfig, embCfg *config.EmbeddingConfig, rateLimitCfg *config.RateLimitConfig) {
	if llmCfg != nil {
		a.llmClient.UpdateConfig(llmCfg)
	}
	if embCfg != nil {
		a.embeddingClient.UpdateConfig(embCfg)
	}
	if rateLimitCfg != nil {
		a.llmRateLimit = rateLimitCfg.LLMInterval
		a.embRateLimit = rateLimitCfg.EmbeddingInterval
	}
}

// SetProxy 设置 LLM/Embedding 接口代理；enableLLM 为 false 或 proxyURL 为空时清除
func (a *Analyzer) SetProxy(proxyURL string, enableLLM bool) {
	url := ""
	if enableLLM {
		url = proxyURL
	}
	a.llmClient.SetProxy(url)
	a.embeddingClient.SetProxy(url)
}

// waitForRateLimit 等待速率限制
func (a *Analyzer) waitForRateLimit(isLLM bool) {
	var lastCall *time.Time
	var interval time.Duration

	if isLLM {
		lastCall = &a.lastLLMCall
		interval = a.llmRateLimit
	} else {
		lastCall = &a.lastEmbCall
		interval = a.embRateLimit
	}

	if !lastCall.IsZero() {
		elapsed := time.Since(*lastCall)
		if elapsed < interval {
			time.Sleep(interval - elapsed)
		}
	}

	now := time.Now()
	if isLLM {
		a.lastLLMCall = now
	} else {
		a.lastEmbCall = now
	}
}

// AnalyzeArticle 分析文章（两阶段：内容分析 + 条件翻译）
func (a *Analyzer) AnalyzeArticle(ctx context.Context, title, content string) (*AnalyzeResult, error) {
	content = TruncateRunes(content, 4000)

	// Phase 1: 内容分析（带缓存）
	a.waitForRateLimit(true)
	// analyze_v3: system/user 分离（利于服务商前缀缓存），key 纳入 system 防止提示词变更后命中旧缓存
	sys := a.analyzeSystemPrompt()
	analyzeKey := buildCacheKey("analyze_v3", a.llmClient.Model(), sys+title+content)
	result, err := a.cache.CachedCall(ctx, analyzeKey, func() (string, error) {
		return a.llmClient.ChatJSONWithSystem(ctx, sys, BuildAnalyzeUserPrompt(title, content))
	})
	if err != nil {
		return nil, fmt.Errorf("LLM analyze failed: %w", err)
	}

	analyzeResult, err := ParseAnalyzeResponse(result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse analyze response: %w", err)
	}

	// Phase 2: 翻译（仅非中文内容触发）
	if !isChineseLang(content) && analyzeResult.TranslatedContent == "" {
		a.waitForRateLimit(true)
		sysT := a.translateSystemPrompt()
		translateKey := buildCacheKey("translate_v2", a.llmClient.Model(), sysT+content)
		translated, err := a.cache.CachedCall(ctx, translateKey, func() (string, error) {
			return a.llmClient.ChatWithSystem(ctx, sysT, BuildTranslateUserPrompt(content))
		})
		if err != nil {
			log.Printf("翻译失败 (非致命): %v", err)
		} else {
			analyzeResult.TranslatedContent = translated
		}
	}

	return analyzeResult, nil
}

// GetEmbedding 获取文章向量（带缓存）
func (a *Analyzer) GetEmbedding(ctx context.Context, text string) ([]byte, error) {
	text = TruncateRunes(text, 8000)

	a.waitForRateLimit(false)

	embKey := buildCacheKey("embedding", a.embeddingClient.Model(), text)
	result, err := a.cache.CachedCall(ctx, embKey, func() (string, error) {
		embedding, err := a.embeddingClient.GetEmbedding(ctx, text)
		if err != nil {
			return "", err
		}
		data, err := SerializeEmbedding(embedding)
		if err != nil {
			return "", err
		}
		return string(data), nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get embedding: %w", err)
	}

	return []byte(result), nil
}

// DefaultTranslateSystemPrompt 翻译的默认 system 提示词（可经 prompts.translate_system 覆盖）
const DefaultTranslateSystemPrompt = `你是一名专业的翻译专家。请将用户提供的文章内容翻译为中文。

要求：
1. 若内容已经是中文，直接原样输出
2. 保留原文中的所有图片链接、超链接、代码块
3. 保留原文的排版格式（标题、列表、表格等）
4. 专有名词和技术术语保持原文，除非有广泛使用的中文译法
5. 不添加任何额外标签、说明或评论
6. 保留原文完整语义，使用自然流畅的中文表达
7. 全文一次性输出，不设长度上限`

// BuildTranslateUserPrompt 构建翻译的用户消息（仅含待翻译内容）
func BuildTranslateUserPrompt(content string) string {
	return content
}


// DefaultAnalyzeSystemPrompt 文章分析的默认 system 提示词
// 指令部分固定不变，与文章内容（user 消息）分离，利于 LLM 服务商的前缀 context caching；
// 可通过 config.yaml 的 prompts.analyze_system 覆盖（设置页可编辑）。
const DefaultAnalyzeSystemPrompt = `你是一名专业的新闻/技术文章分析助手。请分析用户提供的文章，严格按以下要求返回 JSON 结果：

请返回：
{
  "is_ad": false,
  "ad_reason": null,
  "content_cleaned": "清洗后的正文内容",
  "summary": "100-200字摘要",
  "one_line_summary": "一句话总结文章核心内容（20-30字）",
  "keywords": ["关键词1", "关键词2", "关键词3"],
  "tags": ["标签1", "标签2"],
  "entities": ["实体1", "实体2"],
  "importance_score": 7,
  "topic_category": "产品发布",
  "translated_content": "中文翻译（如果原文非中文则为中文翻译，中文原文则留空字符串）"
}

字段说明：
- one_line_summary: 用一句话（20-30字）概括文章核心内容，用于语义匹配。例如："华为发布新款Mate80手机，搭载自研麒麟芯片"
- importance_score: 1-10分，10最重要。评估标准：时效性、行业影响力、信息密度、独特性
- topic_category: 文章主题分类，如"AI动态"、"产品发布"、"行业分析"、"技术教程"、"公司新闻"等
- entities: 文章涉及的主要实体，包括：
  - 公司/组织：如"OpenAI"、"华为"、"腾讯"、"特斯拉"等
  - 人物：如"马斯克"、"黄仁勋"、"奥特曼"等
  - 产品：如"GPT-4"、"Claude"、"Grok"等
  - 国家/地区：如"美国"、"中国"、"伊朗"等
  只提取最重要的3-5个实体
- translated_content: 如果文章原文是英文或其他非中文语言，将正文内容翻译为中文。如果原文是中文则留空字符串。翻译应保留原文的完整语义，使用自然流畅的中文表达。

【正文清洗规则（重要）】
许多文章在开头或结尾夹带与正文无关的频道/账号信息。请在 content_cleaned 中剔除以下内容，只保留正文实质内容：
- 频道/群组介绍与引流：如 "🌸 在花频道"、"茶馆讨论"、"投稿通道"、"关注我们"、"加入群组"、Telegram 频道链接、微信群二维码说明、公众号/社媒账号推广
- 作者签名、栏目落款、免责声明、"往期推荐"、文末广告位
- 采集来源标记：如 "（美联社）"、"来源：xxx" 等仅保留事实本身，除非来源本身是正文语义的一部分
- 纯装饰性表情符号行、分隔线、空口号式结尾（如 "感谢阅读"、"我们下期再见"）
剔除后 content_cleaned 应仍是连贯可读的正文，不要改写或缩写正文本身。

同时，summary、one_line_summary、keywords、tags、entities 必须只基于清洗后的正文生成：
- 不得把频道名、群组名、账号名作为关键词、标签或实体
- 不得在摘要中提及"本频道"、"该群"等推广性表述

【广告/推广内容判断标准】
如果文章符合以下任一特征，请设置 is_ad 为 true，并在 ad_reason 中说明原因：

1. 频道/账号推广：内容主要目的是引导用户关注某个 Telegram 频道、微信群、公众号、社交媒体账号等
   - 标题包含 "📢" 等广播表情符号且内容是频道介绍
   - 内容格式如 "1. 📢 频道名称\n   频道描述"

2. 赌博/博彩广告：包含赌博平台、彩票、体育投注、棋牌游戏等推广
   - 内容包含 "球速"、"体育"、"博彩"、"彩票"、"赌场" 等赌博相关词汇
   - 内容主要由表情符号组成，缺乏实质信息

3. 成人内容推广：推广成人网站、色情内容、性用品等
   - 标题或内容包含 "成人"、"小说推荐"、"性" 等成人内容暗示
   - 推广成人小说、视频、图片等

4. 商业推广：明显的产品推销、服务推广、付费课程等
   - 推广具体产品或服务并带有购买引导
   - "合规"、"税务筹划"等服务推广

5. 垃圾信息：内容空洞、无实际价值
   - 内容主要由表情符号组成，无实质文字内容
   - 转发的垃圾信息（Forwarded From Bot）

注意：正常的知识分享、新闻资讯、技术文章不是广告，即使文末有频道链接也不应标记为广告。

【广告判断补充规则】
只有当你"非常确信"文章是广告/推广时才设为 true。
如果只是疑似或没有把握，请设为 false。
宁可漏过也不要误判正常文章。`

// BuildAnalyzeUserPrompt 构建文章分析的用户消息（仅含文章数据，与 system 指令分离）
func BuildAnalyzeUserPrompt(title, content string) string {
	return fmt.Sprintf("文章标题：%s\n\n文章内容：\n%s", title, content)
}

// ParseAnalyzeResponse 解析 AI 分析响应
func ParseAnalyzeResponse(response string) (*AnalyzeResult, error) {
	// 使用公共函数提取 JSON
	response = ExtractJSONFromResponse(response)

	var result AnalyzeResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("failed to parse analyze response: %w (response: %s)", err, response)
	}

	// 设置默认值
	if result.ImportanceScore == 0 {
		result.ImportanceScore = 5
	}

	return &result, nil
}

// CalculateCosineSimilarity 计算两个向量的余弦相似度
func CalculateCosineSimilarity(vec1, vec2 []float32) float64 {
	if len(vec1) != len(vec2) {
		panic("vectors must have the same length")
	}

	var dotProduct, norm1, norm2 float64
	for i := range vec1 {
		dotProduct += float64(vec1[i]) * float64(vec2[i])
		norm1 += float64(vec1[i]) * float64(vec1[i])
		norm2 += float64(vec2[i]) * float64(vec2[i])
	}

	if norm1 == 0 || norm2 == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(norm1) * math.Sqrt(norm2))
}

// Chat 直接调用 LLM 聊天
func (a *Analyzer) Chat(ctx context.Context, prompt string) (string, error) {
	a.waitForRateLimit(true)
	return a.llmClient.Chat(ctx, prompt)
}

// ChatJSON 调用 LLM 聊天（JSON 输出模式）
func (a *Analyzer) ChatJSON(ctx context.Context, prompt string) (string, error) {
	a.waitForRateLimit(true)
	return a.llmClient.ChatJSON(ctx, prompt)
}

// ChatWithDeepThinking 调用 LLM 聊天（深度思考模式）
func (a *Analyzer) ChatWithDeepThinking(ctx context.Context, prompt string, thinkingBudget int) (string, error) {
	a.waitForRateLimit(true)
	return a.llmClient.ChatWithDeepThinking(ctx, prompt, thinkingBudget)
}

// ChatJSONWithDeepThinking 调用 LLM 聊天（JSON + 深度思考模式）
func (a *Analyzer) ChatJSONWithDeepThinking(ctx context.Context, prompt string, thinkingBudget int) (string, error) {
	a.waitForRateLimit(true)
	return a.llmClient.ChatJSONWithDeepThinking(ctx, prompt, thinkingBudget)
}

// ArticleInfo 文章元信息（用于聚类）
type ArticleInfo struct {
	ID       int64
	Title    string
	Category string
	Link     string
}

// ClusterResult 聚类结果
type ClusterResult struct {
	Topics []ClusterTopic `json:"topics"`
}

// ClusterTopic 聚类主题
type ClusterTopic struct {
	Name       string `json:"name"`
	ArticleIDs []int  `json:"article_ids"`
	IsFeatured bool   `json:"is_featured"`
	Reason     string `json:"reason"`
}

// BuildClusterPrompt 构建文章聚类提示词
func BuildClusterPrompt(articles []ArticleInfo) string {
	var sb strings.Builder
	sb.WriteString("你是一个新闻编辑，需要将以下文章按主题分组，并选出重点。\n\n")
	sb.WriteString("文章列表：\n")

	for i, a := range articles {
		category := a.Category
		if category == "" {
			category = "未分类"
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, category, a.Title))
	}

	sb.WriteString(`
请返回JSON：
{
  "topics": [
    {
      "name": "主题名称",
      "article_ids": [1, 3],
      "is_featured": true,
      "reason": "选为重点的原因"
    }
  ]
}

要求：
- is_featured=true 的主题最多5个（重点主题）
- 合并相似主题，每个主题至少2篇文章或内容足够重要
- topic name 要有新闻感，如"AI模型竞赛升温"而非"AI新闻"
- article_ids 使用上面列表中的序号（从1开始）`)

	return sb.String()
}

// ParseClusterResponse 解析聚类响应
func ParseClusterResponse(response string) (*ClusterResult, error) {
	// 使用公共函数提取 JSON
	response = ExtractJSONFromResponse(response)

	var result ClusterResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("failed to parse cluster response: %w", err)
	}

	return &result, nil
}

// BuildFeaturedPrompt 构建重点文章生成提示词
func BuildFeaturedPrompt(topicName string, articles []ArticleInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("你是资讯编辑，为\"%s\"主题撰写早报内容。\n\n", topicName))
	sb.WriteString("相关文章：\n")

	for _, a := range articles {
		sb.WriteString(fmt.Sprintf("- %s (%s)\n", a.Title, a.Link))
	}

	sb.WriteString(fmt.Sprintf(`
请撰写：
1. 正文（500字内）：客观报道事实，末尾加入1-2句分析洞察
2. 知识点（3条）：提炼关键信息点
3. 来源链接

格式要求：
## %s

{{正文内容，500字内}}

**知识点：**
- 要点1
- 要点2
- 要点3

**来源：** [[1]](链接1 "标题1"), [[2]](链接2 "标题2")`, topicName))

	return sb.String()
}

// BuildBriefPrompt 构建简讯生成提示词
func BuildBriefPrompt(topicName string, articles []ArticleInfo, index int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("你是资讯编辑，为\"%s\"主题撰写简讯。\n\n", topicName))
	sb.WriteString("相关文章：\n")

	for _, a := range articles {
		sb.WriteString(fmt.Sprintf("- %s (%s)\n", a.Title, a.Link))
	}

	sb.WriteString(fmt.Sprintf(`
请撰写100字以内的简讯描述，包含核心事实。

格式：
**%d %s** — {{100字描述}} [[1]](链接1 "标题1")`, index, topicName))

	return sb.String()
}

// TruncateRunes 按字节预算截断字符串，在 UTF-8 字符边界处切断，避免中文出现乱码
func TruncateRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}
