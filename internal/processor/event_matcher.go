package processor

import (
	"fmt"
	"log"
	"math"
	"regexp"
	"rss-ai/internal/ai"
	"rss-ai/internal/database"
	"rss-ai/internal/models"
	"strings"
	"time"
	"unicode"
)

// EventMatcher 事件匹配器
type EventMatcher struct {
	db                  *database.DB
	analyzer            *ai.Analyzer
	keywordWeight       float64 // 关键词匹配权重
	vectorWeight        float64 // 向量匹配权重
	threshold           float64 // 匹配阈值
	vectorMinSimilarity float64 // 向量相似度最低阈值
}

// NewEventMatcher 创建事件匹配器
func NewEventMatcher(db *database.DB, analyzer *ai.Analyzer, matchThreshold, vectorMinSimilarity, keywordWeight, vectorWeight float64) *EventMatcher {
	// 使用默认值如果传入值为0
	if matchThreshold <= 0 {
		matchThreshold = 0.50
	}
	if vectorMinSimilarity <= 0 {
		vectorMinSimilarity = 0.40
	}
	if keywordWeight <= 0 {
		keywordWeight = 0.4
	}
	if vectorWeight <= 0 {
		vectorWeight = 0.6
	}

	return &EventMatcher{
		db:                  db,
		analyzer:            analyzer,
		keywordWeight:       keywordWeight,
		vectorWeight:        vectorWeight,
		threshold:           matchThreshold,
		vectorMinSimilarity: vectorMinSimilarity,
	}
}

// MatchResult 匹配结果
type MatchResult struct {
	EventID      int64
	EventName    string
	MatchScore   float64
	MatchReason  string // "keyword", "vector", "both"
	KeywordScore float64
	VectorScore  float64
}

// MatchArticleToEvents 将文章匹配到事件
func (m *EventMatcher) MatchArticleToEvents(articleID int64, title, content string, articleEmbedding []byte, summaryEmbedding []byte) error {
	// 获取所有活跃事件
	events, err := m.db.GetActiveEventTracks()
	if err != nil {
		return fmt.Errorf("failed to get active events: %w", err)
	}

	if len(events) == 0 {
		return nil
	}

	// 优先使用总结向量（更精确），如果没有则使用全文向量
	var articleVec []float32
	if len(summaryEmbedding) > 0 {
		articleVec, err = ai.DeserializeEmbedding(summaryEmbedding)
		if err != nil {
			log.Printf("Failed to deserialize summary embedding: %v", err)
		}
	}
	if len(articleVec) == 0 && len(articleEmbedding) > 0 {
		articleVec, err = ai.DeserializeEmbedding(articleEmbedding)
		if err != nil {
			log.Printf("Failed to deserialize article embedding: %v", err)
		}
	}

	// 对每个事件进行匹配
	for _, event := range events {
		result := m.matchArticleToEvent(articleID, title, content, articleVec, event)
		// 调试日志：输出每个事件的匹配详情
		if result != nil {
			log.Printf("DEBUG: Article %d vs Event %s: score=%.3f (threshold=%.2f), keywordScore=%.3f, vectorScore=%.3f, reason=%s",
				articleID, event.Name, result.MatchScore, m.threshold, result.KeywordScore, result.VectorScore, result.MatchReason)
		}
		if result != nil && result.MatchScore >= m.threshold {
			// 匹配成功，创建关联
			if err := m.createEventArticle(articleID, event.ID, result); err != nil {
				log.Printf("Failed to create event article: %v", err)
				continue
			}
			log.Printf("Article %d matched to event %s (score: %.2f, reason: %s)",
				articleID, event.Name, result.MatchScore, result.MatchReason)
		}
	}

	return nil
}

// MatchArticleToSingleEvent 将文章匹配到单个指定事件
func (m *EventMatcher) MatchArticleToSingleEvent(articleID int64, title, content string, articleEmbedding []byte, summaryEmbedding []byte, event *models.EventTrack) error {
	// 优先使用总结向量（更精确），如果没有则使用全文向量
	var articleVec []float32
	var err error
	if len(summaryEmbedding) > 0 {
		articleVec, err = ai.DeserializeEmbedding(summaryEmbedding)
		if err != nil {
			log.Printf("Failed to deserialize summary embedding: %v", err)
		}
	}
	if len(articleVec) == 0 && len(articleEmbedding) > 0 {
		articleVec, err = ai.DeserializeEmbedding(articleEmbedding)
		if err != nil {
			log.Printf("Failed to deserialize article embedding: %v", err)
		}
	}

	result := m.matchArticleToEvent(articleID, title, content, articleVec, event)
	if result != nil && result.MatchScore >= m.threshold {
		// 匹配成功，创建关联
		if err := m.createEventArticle(articleID, event.ID, result); err != nil {
			return fmt.Errorf("failed to create event article: %w", err)
		}
		log.Printf("Article %d matched to event %s (score: %.2f, reason: %s)",
			articleID, event.Name, result.MatchScore, result.MatchReason)
	}

	return nil
}

// matchArticleToEvent 计算文章与事件的匹配度
func (m *EventMatcher) matchArticleToEvent(articleID int64, title, content string, articleVec []float32, event *models.EventTrack) *MatchResult {
	result := &MatchResult{
		EventID:   event.ID,
		EventName: event.Name,
	}

	// 1. 关键词匹配（正向）
	keywordScore := m.calculateKeywordScore(title, content, event.Keywords)
	result.KeywordScore = keywordScore

	// 2. 负面关键词匹配（扣分）
	negativeScore := m.calculateNegativeKeywordScore(title, content, event.NegativeKeywords)

	// 3. 向量匹配
	vectorScore := 0.0
	if len(articleVec) > 0 && len(event.Embedding) > 0 {
		eventVec, err := ai.DeserializeEmbedding(event.Embedding)
		if err == nil {
			vectorScore = m.calculateVectorScore(articleVec, eventVec)
		}
	}
	result.VectorScore = vectorScore

	// 4. 综合得分 = 正向关键词 + 向量 - 负面关键词扣分
	result.MatchScore = m.keywordWeight*keywordScore + m.vectorWeight*vectorScore - negativeScore

	// 确保分数不为负
	if result.MatchScore < 0 {
		result.MatchScore = 0
	}

	// 5. 确定匹配原因
	if keywordScore >= 0.5 && vectorScore >= 0.5 {
		result.MatchReason = "both"
	} else if keywordScore >= 0.5 {
		result.MatchReason = "keyword"
	} else if vectorScore >= 0.5 {
		result.MatchReason = "vector"
	} else {
		result.MatchReason = "weak"
	}

	// 如果有负面关键词匹配，标记原因
	if negativeScore > 0 {
		result.MatchReason = result.MatchReason + "-negative"
	}

	return result
}

// calculateKeywordScore 计算关键词匹配得分
func (m *EventMatcher) calculateKeywordScore(title, content, keywords string) float64 {
	if keywords == "" {
		return 0
	}

	// 合并标题和内容（标题权重更高，重复计算）
	text := strings.ToLower(title + " " + title + " " + content)

	// 统计匹配的关键词数量
	keywordList := strings.Split(keywords, ",")
	if len(keywordList) == 0 {
		return 0
	}

	matchedCount := 0
	for _, kw := range keywordList {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		if strings.Contains(text, strings.ToLower(kw)) {
			matchedCount++
		}
	}

	// 新逻辑：只要匹配1个关键词就算有效，使用平滑的得分曲线
	if matchedCount == 0 {
		return 0
	}

	// 基础分：匹配至少1个关键词
	// 使用对数曲线： 1个关键词 = 0.4, 2个 = 0.6, 3个 = 0.75, 4个 = 0.8...
	// 公式: score = matchedCount / (matchedCount + 1)
	baseScore := float64(matchedCount) / float64(matchedCount+1)

	// 如果匹配的关键词数量较多，给予额外奖励
	if matchedCount >= 3 {
		baseScore = math.Min(baseScore+0.1, 1.0)
	}

	return baseScore
}

// calculateNegativeKeywordScore 计算负面关键词扣分
func (m *EventMatcher) calculateNegativeKeywordScore(title, content, negativeKeywords string) float64 {
	if negativeKeywords == "" {
		return 0
	}

	// 合并标题和内容（标题权重更高）
	text := strings.ToLower(title + " " + title + " " + content)

	// 统计匹配的负面关键词数量
	keywordList := strings.Split(negativeKeywords, ",")
	if len(keywordList) == 0 {
		return 0
	}

	matchedCount := 0
	for _, kw := range keywordList {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		if strings.Contains(text, strings.ToLower(kw)) {
			matchedCount++
		}
	}

	if matchedCount == 0 {
		return 0
	}

	// 负面关键词扣分：使用与正向关键词相同的曲线，但惩罚力度加倍
	// matchedCount=1 -> 0.50 * 2 = 1.0
	// matchedCount=2 -> 0.67 * 2 = 1.34
	// matchedCount=3 -> 0.75 * 2 = 1.5
	baseScore := float64(matchedCount) / float64(matchedCount+1)

	// 惩罚力度加倍
	penalty := baseScore * 2.0

	// 最多扣 1.0 分（避免分数变成负数）
	if penalty > 1.0 {
		penalty = 1.0
	}

	return penalty
}

// calculateVectorScore 计算向量相似度
func (m *EventMatcher) calculateVectorScore(vec1, vec2 []float32) float64 {
	if len(vec1) != len(vec2) {
		return 0
	}

	// 余弦相似度
	var dotProduct, norm1, norm2 float64
	for i := range vec1 {
		dotProduct += float64(vec1[i]) * float64(vec2[i])
		norm1 += float64(vec1[i]) * float64(vec1[i])
		norm2 += float64(vec2[i]) * float64(vec2[i])
	}

	if norm1 == 0 || norm2 == 0 {
		return 0
	}

	similarity := dotProduct / (math.Sqrt(norm1) * math.Sqrt(norm2))

	// 直接使用余弦相似度（范围-1到1），但只取正值部分
	// 相似度<配置的最低阈值表示不相关，得分0
	// 相似度>minSimilarity才算弱相关，得分为 (similarity - minSimilarity) / (1 - minSimilarity)
	minSimilarity := m.vectorMinSimilarity
	if similarity < minSimilarity {
		return 0
	}

	// 将相似度从 [minSimilarity, 1] 映射到 [0, 1]
	score := (similarity - minSimilarity) / (1 - minSimilarity)
	if score > 1 {
		score = 1
	}
	return score
}

// isLowQualityArticle 检查文章是否为低质量（应被过滤）
func isLowQualityArticle(article *models.Article) bool {
	// 1. 检查标题是否为空或只有emoji
	title := strings.TrimSpace(article.Title)
	if title == "" {
		return true
	}
	if isOnlyEmoji(title) {
		return true
	}

	// 2. 检查正文是否为纯图片（content_cleaned为空且content只有img标签）
	content := strings.TrimSpace(article.Content)
	contentCleaned := strings.TrimSpace(article.ContentCleaned)
	if contentCleaned == "" && isOnlyImageTag(content) {
		return true
	}

	return false
}

// isOnlyEmoji 检查字符串是否只包含emoji和空白字符
func isOnlyEmoji(s string) bool {
	// 移除所有空白字符
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = strings.ReplaceAll(s, "\n", "")
	if s == "" {
		return true
	}

	// 检查是否所有字符都是emoji或符号
	for _, r := range s {
		// emoji 通常在以下 Unicode 范围内
		if !isEmojiRune(r) {
			return false
		}
	}
	return true
}

// isEmojiRune 判断字符是否为emoji
func isEmojiRune(r rune) bool {
	// 常见emoji范围
	if r >= 0x1F600 && r <= 0x1F64F { // Emoticons
		return true
	}
	if r >= 0x1F300 && r <= 0x1F5FF { // Misc Symbols and Pictographs
		return true
	}
	if r >= 0x1F680 && r <= 0x1F6FF { // Transport and Map
		return true
	}
	if r >= 0x1F1E0 && r <= 0x1F1FF { // Flags
		return true
	}
	if r >= 0x2600 && r <= 0x26FF { // Misc symbols
		return true
	}
	if r >= 0x2700 && r <= 0x27BF { // Dingbats
		return true
	}
	if r >= 0xFE00 && r <= 0xFE0F { // Variation Selectors
		return true
	}
	if r >= 0x1F900 && r <= 0x1F9FF { // Supplemental Symbols and Pictographs
		return true
	}
	if r >= 0x1FA00 && r <= 0x1FA6F { // Chess Symbols
		return true
	}
	if r >= 0x1FA70 && r <= 0x1FAFF { // Symbols and Pictographs Extended-A
		return true
	}
	if r >= 0x231A && r <= 0x231B { // Watch, Hourglass
		return true
	}
	if r >= 0x23E9 && r <= 0x23F3 { // Various
		return true
	}
	if r >= 0x23F8 && r <= 0x23FA { // Various
		return true
	}
	if r >= 0x25AA && r <= 0x25AB { // Squares
		return true
	}
	if r >= 0x25B6 && r <= 0x25C0 { // Triangles
		return true
	}
	if r >= 0x25FB && r <= 0x25FE { // Squares
		return true
	}
	if r >= 0x2614 && r <= 0x2615 { // Umbrella, Hot Beverage
		return true
	}
	if r >= 0x2648 && r <= 0x2653 { // Zodiac
		return true
	}
	if r >= 0x267F && r <= 0x267F { // Wheelchair
		return true
	}
	if r >= 0x2693 && r <= 0x2693 { // Anchor
		return true
	}
	if r >= 0x26A1 && r <= 0x26A1 { // High Voltage
		return true
	}
	if r >= 0x26AA && r <= 0x26AB { // Circles
		return true
	}
	if r >= 0x26BD && r <= 0x26BE { // Sports
		return true
	}
	if r >= 0x26C4 && r <= 0x26C5 { // Snowman, Sun
		return true
	}
	if r >= 0x26CE && r <= 0x26CE { // Ophiuchus
		return true
	}
	if r >= 0x26D4 && r <= 0x26D4 { // No Entry
		return true
	}
	if r >= 0x26EA && r <= 0x26EA { // Church
		return true
	}
	if r >= 0x26F2 && r <= 0x26F3 { // Fountain, Golf
		return true
	}
	if r >= 0x26F5 && r <= 0x26F5 { // Sailboat
		return true
	}
	if r >= 0x26FA && r <= 0x26FA { // Tent
		return true
	}
	if r >= 0x26FD && r <= 0x26FD { // Fuel Pump
		return true
	}
	if r >= 0x2702 && r <= 0x2702 { // Scissors
		return true
	}
	if r >= 0x2705 && r <= 0x2705 { // Check Mark
		return true
	}
	if r >= 0x2708 && r <= 0x270D { // Various
		return true
	}
	if r >= 0x270F && r <= 0x270F { // Pencil
		return true
	}
	if r >= 0x2712 && r <= 0x2712 { // Black Nib
		return true
	}
	if r >= 0x2714 && r <= 0x2714 { // Check Mark
		return true
	}
	if r >= 0x2716 && r <= 0x2716 { // X Mark
		return true
	}
	if r >= 0x271D && r <= 0x271D { // Cross
		return true
	}
	if r >= 0x2721 && r <= 0x2721 { // Star of David
		return true
	}
	if r >= 0x2728 && r <= 0x2728 { // Sparkles
		return true
	}
	if r >= 0x2733 && r <= 0x2734 { // Eight-Spoked Asterisk
		return true
	}
	if r >= 0x2744 && r <= 0x2744 { // Snowflake
		return true
	}
	if r >= 0x2747 && r <= 0x2747 { // Sparkle
		return true
	}
	if r >= 0x274C && r <= 0x274C { // Cross Mark
		return true
	}
	if r >= 0x274E && r <= 0x274E { // Cross Mark
		return true
	}
	if r >= 0x2753 && r <= 0x2755 { // Question Mark
		return true
	}
	if r >= 0x2757 && r <= 0x2757 { // Exclamation Mark
		return true
	}
	if r >= 0x2763 && r <= 0x2764 { // Heart Exclamation, Heart
		return true
	}
	if r >= 0x2795 && r <= 0x2797 { // Math
		return true
	}
	if r >= 0x27A1 && r <= 0x27A1 { // Arrow
		return true
	}
	if r >= 0x27B0 && r <= 0x27B0 { // Curly Loop
		return true
	}
	if r >= 0x27BF && r <= 0x27BF { // Double Curly Loop
		return true
	}
	if r >= 0x2934 && r <= 0x2935 { // Arrows
		return true
	}
	if r >= 0x2B05 && r <= 0x2B07 { // Arrows
		return true
	}
	if r >= 0x2B1B && r <= 0x2B1C { // Squares
		return true
	}
	if r >= 0x2B50 && r <= 0x2B50 { // Star
		return true
	}
	if r >= 0x2B55 && r <= 0x2B55 { // Circle
		return true
	}
	if r >= 0x3030 && r <= 0x3030 { // Wavy Dash
		return true
	}
	if r >= 0x303D && r <= 0x303D { // Part Alternation Mark
		return true
	}
	if r >= 0x3297 && r <= 0x3297 { // Circled Ideograph Congratulation
		return true
	}
	if r >= 0x3299 && r <= 0x3299 { // Circled Ideograph Secret
		return true
	}
	// 变体选择符
	if r >= 0xFE00 && r <= 0xFE0F {
		return true
	}
	// 零宽连接符（用于组合emoji）
	if r == 0x200D {
		return true
	}
	return false
}

// isOnlyImageTag 检查内容是否只包含img标签（纯图片）
func isOnlyImageTag(content string) bool {
	if content == "" {
		return false
	}

	// 移除所有空白字符
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}

	// 使用正则匹配img标签，检查是否只剩下img标签
	// 匹配 <img ...> 或 <img ... />
	imgPattern := regexp.MustCompile(`(?i)<img[^>]*\/?>`)
	stripped := imgPattern.ReplaceAllString(content, "")
	stripped = strings.TrimSpace(stripped)

	// 如果移除img标签后内容为空，说明是纯图片
	return stripped == ""
}

// isLetterOrDigit 判断字符是否为字母或数字（未使用，但保留供参考）
func isLetterOrDigit(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// createEventArticle 创建事件-文章关联并提取角色
func (m *EventMatcher) createEventArticle(articleID, eventID int64, result *MatchResult) error {
	// 检查是否已存在
	exists, err := m.db.CheckArticleInEvent(eventID, articleID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	// 获取文章内容
	article, err := m.db.GetArticleByID(articleID)
	if err != nil {
		return err
	}

	// 文章质量检查：排除低质量文章
	if isLowQualityArticle(article) {
		log.Printf("Article %d skipped: low quality (empty/emoji-only title or image-only content)", articleID)
		return nil
	}

	// 获取事件信息（用于获取预设角色）
	event, err := m.db.GetEventTrack(eventID)
	if err != nil {
		return err
	}

	// 从文章 entities 和事件预设角色中匹配
	role := m.matchRole(article.Entities, event.Roles)

	// 创建关联
	ea := &models.EventArticle{
		EventID:     eventID,
		ArticleID:   articleID,
		Role:        role,
		Importance:  article.ImportanceScore,
		MatchReason: result.MatchReason,
		MatchScore:  result.MatchScore,
		CreatedAt:   time.Now(),
	}

	insertedID, err := m.db.CreateEventArticle(ea)
	if err != nil {
		return err
	}

	// 只有实际插入新记录时才更新计数（INSERT OR IGNORE 返回0表示已存在）
	if insertedID > 0 {
		// 更新事件匹配计数
		m.db.IncrementEventMatchCount(eventID)

		// 更新事件向量（滚动平均）
		if len(article.Embedding) > 0 {
			m.updateEventEmbedding(eventID, article.Embedding)
		}
	}

	return nil
}

// matchRole 从文章实体中匹配事件预设角色
func (m *EventMatcher) matchRole(articleEntities, eventRoles string) string {
	if articleEntities == "" || eventRoles == "" {
		return ""
	}

	// 解析文章实体
	entities := strings.Split(articleEntities, ",")
	entityMap := make(map[string]bool)
	for _, e := range entities {
		e = strings.TrimSpace(e)
		if e != "" {
			entityMap[strings.ToLower(e)] = true
		}
	}

	// 解析事件预设角色，找到匹配的
	roles := strings.Split(eventRoles, ",")
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		// 检查文章实体中是否包含该角色
		if entityMap[strings.ToLower(role)] {
			return role
		}
		// 检查部分匹配（如"马斯克"匹配"马斯克/特斯拉"）
		for entity := range entityMap {
			if strings.Contains(entity, strings.ToLower(role)) || strings.Contains(strings.ToLower(role), entity) {
				return role
			}
		}
	}

	// 如果没有匹配到预设角色，返回第一个实体作为角色（用于统计）
	if len(entities) > 0 {
		firstEntity := strings.TrimSpace(entities[0])
		if firstEntity != "" {
			return firstEntity
		}
	}

	return ""
}

// updateEventEmbedding 更新事件向量（滚动平均）
func (m *EventMatcher) updateEventEmbedding(eventID int64, newArticleEmbedding []byte) {
	// 获取当前事件
	event, err := m.db.GetEventTrack(eventID)
	if err != nil {
		return
	}

	newVec, err := ai.DeserializeEmbedding(newArticleEmbedding)
	if err != nil {
		return
	}

	var updatedVec []float32
	if len(event.Embedding) == 0 {
		// 第一次，直接使用新向量
		updatedVec = newVec
	} else {
		// 滚动平均：新向量 = 0.7 * 旧向量 + 0.3 * 新向量
		oldVec, err := ai.DeserializeEmbedding(event.Embedding)
		if err != nil {
			return
		}
		if len(oldVec) != len(newVec) {
			return
		}
		updatedVec = make([]float32, len(oldVec))
		for i := range oldVec {
			updatedVec[i] = float32(0.7)*oldVec[i] + float32(0.3)*newVec[i]
		}
	}

	embeddingBytes, err := ai.SerializeEmbedding(updatedVec)
	if err != nil {
		return
	}

	m.db.UpdateEventTrackEmbedding(eventID, embeddingBytes)
}
