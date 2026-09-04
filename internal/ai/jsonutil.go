package ai

import "strings"

// ExtractJSONFromResponse 从 LLM 响应中提取 JSON 内容
// 处理 markdown 代码块和纯 JSON 两种情况
func ExtractJSONFromResponse(response string) string {
	response = strings.TrimSpace(response)

	// 处理 markdown 代码块
	if strings.Contains(response, "```json") {
		start := strings.Index(response, "```json") + 7
		if end := strings.Index(response[start:], "```"); end > 0 {
			response = response[start : start+end]
		}
	} else if strings.Contains(response, "```") {
		start := strings.Index(response, "```") + 3
		// 跳过可能的语言标识符（如 ```javascript）
		if newlineIdx := strings.Index(response[start:], "\n"); newlineIdx >= 0 && newlineIdx < 20 {
			start += newlineIdx + 1
		}
		if end := strings.Index(response[start:], "```"); end > 0 {
			response = response[start : start+end]
		}
	}

	// 提取 JSON 对象
	startIdx := strings.Index(response, "{")
	endIdx := strings.LastIndex(response, "}")
	if startIdx >= 0 && endIdx > startIdx {
		return response[startIdx : endIdx+1]
	}

	// 尝试提取 JSON 数组
	startIdx = strings.Index(response, "[")
	endIdx = strings.LastIndex(response, "]")
	if startIdx >= 0 && endIdx > startIdx {
		return response[startIdx : endIdx+1]
	}

	return strings.TrimSpace(response)
}
