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

	// 提取 JSON 对象（平衡括号扫描：只取第一个完整对象）
	startIdx := strings.Index(response, "{")
	if startIdx >= 0 {
		if endIdx := balancedJSONObjectEnd(response[startIdx:]); endIdx > 0 {
			return response[startIdx : startIdx+endIdx]
		}
		// 括号不平衡：退回旧的「首 { 至末 }」截取，行为与旧版一致（交给解析器报错）
		if lastIdx := strings.LastIndex(response, "}"); lastIdx > startIdx {
			return response[startIdx : lastIdx+1]
		}
	}

	// 尝试提取 JSON 数组
	startIdx = strings.Index(response, "[")
	endIdx := strings.LastIndex(response, "]")
	if startIdx >= 0 && endIdx > startIdx {
		return response[startIdx : endIdx+1]
	}

	return strings.TrimSpace(response)
}

// balancedJSONObjectEnd 返回 s 中首个平衡 JSON 对象的结束位置（不含）。
// 引号感知：跳过字符串字面量内的 {} 与转义序列。
// LLM 偶发把同一 JSON 输出两遍（{obj},{obj}），旧的「首 { 至末 }」截取
// 会把两段拼在一起，json.Unmarshal 报 "invalid character ',' after top-level value"。
func balancedJSONObjectEnd(s string) int {
	depth := 0
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if c == '\\' {
				i++ // 跳过转义字符
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// UnmarshalLenient 解析 JSON；失败时修复 LLM 输出中的常见畸形后重试，
// 修复仍失败则返回原始错误（失败模式与严格解析一致，不引入回归）。
func UnmarshalLenient(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		if err2 := json.Unmarshal([]byte(repairLLMJSON(string(data))), v); err2 != nil {
			return err
		}
	}
	return nil
}

func isHex4(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return len(s) == 4
}

// repairLLMJSON 修复 LLM 输出 JSON 中的常见畸形（仅在严格解析失败后兜底）：
//  1. 字符串值内未转义的双引号（如 首款"中折叠"手机）——转义为 \"
//  2. 值内非法转义序列（如正文里的 \sigma、路径 ~\ ）——补成 \\X，解码后为字面反斜杠
//  3. 值结束后全角逗号"，"误作键值分隔符（如 "summary": "xx"，）——归一为 ASCII 逗号
//
// 判定引号是否真结束不能只看下一个字符：值内会出现 `升高 ", 随后` 这类
// 「内嵌引号 + ASCII 逗号」，须前瞻逗号后的非空白字符（必须是下个键的引号
// 或对象收尾才算真结束）。键串以 : 收尾、数组元素串以 , ] 收尾，分别判定。
func repairLLMJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)

	inString := false
	isKey := false   // 当前字符串是对象键（: 收尾）还是值（, } 收尾）
	var stack []byte // '{' 或 '[' 嵌套栈：栈顶为 [ 时字符串是数组元素
	var lastSig byte // 最近写出的结构性字符（{ [ : , } ]），用于区分键/值

	skipWS := func(i int) int {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
		return i
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if c == '\\' && i+1 < len(s) {
				next := s[i+1]
				switch next {
				case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
					b.WriteByte(c)
					i++
					b.WriteByte(next)
				case 'u':
					if i+6 <= len(s) && isHex4(s[i+2:i+6]) {
						b.WriteString(s[i : i+6])
						i += 5
					} else {
						b.WriteString(`\\`)
						i++
						b.WriteByte(next)
					}
				default:
					// 非法转义（\s、\<空格> 等）：补成 \\X，保留字面反斜杠
					b.WriteString(`\\`)
					i++
					b.WriteByte(next)
				}
				continue
			}
			if c == '"' {
				// 前瞻判定真结束
				j := skipWS(i + 1)
				terminated := false
				switch {
				case j >= len(s):
					terminated = true
				case isKey:
					terminated = s[j] == ':'
				case len(stack) > 0 && stack[len(stack)-1] == '[':
					terminated = s[j] == ',' || s[j] == ']'
				default: // 对象值
					switch s[j] {
					case ',', '}', ']':
						k := skipWS(j + 1)
						terminated = s[j] != ',' || k >= len(s) || s[k] == '"' || s[k] == '}'
					case 0xEF:
						// 全角逗号"，"(U+FF0C, EF BC 8C) 误作分隔符：
						// 后随下个键或对象收尾才算真结束并替换为 ASCII 逗号
						if j+2 < len(s) && s[j+1] == 0xBC && s[j+2] == 0x8C {
							k := skipWS(j + 3)
							if k < len(s) && (s[k] == '"' || s[k] == '}') {
								b.WriteByte('"')
								inString = false
								b.WriteByte(',')
								lastSig = ',' // 合成逗号绕过了外层 switch，须手动记录
								i = j + 2     // 主循环 i++ 跳过全角逗号最后一字节
								continue
							}
						}
					}
				}
				if terminated {
					b.WriteByte(c)
					inString = false
				} else {
					b.WriteString(`\"`)
				}
				continue
			}
			b.WriteByte(c)
			continue
		}

		switch c {
		case '"':
			inString = true
			isKey = (lastSig == ',' || lastSig == '{')
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				isKey = false
			}
		case '{', '[':
			stack = append(stack, c)
			lastSig = c
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			lastSig = c
		case ':', ',':
			lastSig = c
		}
		b.WriteByte(c)
	}
	return b.String()
}
