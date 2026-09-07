package ai

import (
	"encoding/json"
	"strings"
)

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

// UnmarshalLenient 解析 JSON；失败时修复字符串值内未转义引号后重试，
// 修复仍失败则返回原始错误（失败模式与严格解析一致，不引入回归）。
func UnmarshalLenient(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		if err2 := json.Unmarshal([]byte(RepairUnescapedQuotes(string(data))), v); err2 != nil {
			return err
		}
	}
	return nil
}

// RepairUnescapedQuotes 修复 JSON 字符串值内未转义的双引号。
// LLM 偶发在文本值里输出裸引号（如 首款"中折叠"手机），使字符串提前结束，
// json.Unmarshal 报 "invalid character ... after object key:value pair"。
// 判定：字符串内遇到引号时，向后跳过空白看第一个字符，
// 不是 , } ] :（key/值结束后的合法结构字符）则视为内嵌引号并转义。
func RepairUnescapedQuotes(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			b.WriteByte(c)
			continue
		}
		if c == '\\' && i+1 < len(s) { // 转义序列原样保留；末尾孤立 \ 走兜底写入
			b.WriteByte(c)
			i++
			b.WriteByte(s[i])
			continue
		}
		if c == '"' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j >= len(s) || s[j] == ',' || s[j] == '}' || s[j] == ']' || s[j] == ':' {
				inString = false
				b.WriteByte(c)
			} else {
				b.WriteString(`\"`)
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
