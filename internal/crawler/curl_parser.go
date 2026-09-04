package crawler

import (
	"fmt"
	"regexp"
	"strings"
)

// CurlParseResult curl 命令解析结果
type CurlParseResult struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// ParseCurlCommand 解析 curl 命令
func ParseCurlCommand(cmd string) (*CurlParseResult, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil, fmt.Errorf("empty curl command")
	}

	result := &CurlParseResult{
		Method:  "GET",
		Headers: make(map[string]string),
	}

	// 移除换行续行符
	cmd = strings.ReplaceAll(cmd, "\\\n", " ")
	cmd = strings.ReplaceAll(cmd, "\\\r\n", " ")

	// 提取 URL
	urlRe := regexp.MustCompile(`curl\s+(?:-[^\s]+\s+)*['"]?(https?://[^\s'"]+)['"]?`)
	if matches := urlRe.FindStringSubmatch(cmd); len(matches) > 1 {
		result.URL = matches[1]
	}

	// 提取 method: -X POST
	methodRe := regexp.MustCompile(`-X\s+(\w+)`)
	if matches := methodRe.FindStringSubmatch(cmd); len(matches) > 1 {
		result.Method = matches[1]
	}

	// 提取 headers: -H 'Key: Value'
	headerRe := regexp.MustCompile(`-H\s+['"]([^'"]+)['"]`)
	headerMatches := headerRe.FindAllStringSubmatch(cmd, -1)
	for _, m := range headerMatches {
		parts := strings.SplitN(m[1], ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			result.Headers[key] = value
		}
	}

	// 提取 body: --data, --data-raw, -d
	// 使用两种引号分别匹配，确保首尾引号一致
	bodyReSingle := regexp.MustCompile(`(?:--data(?:-raw)?|-d)\s+'([^']*)'`)
	if matches := bodyReSingle.FindStringSubmatch(cmd); len(matches) > 1 {
		result.Body = matches[1]
	} else {
		bodyReDouble := regexp.MustCompile(`(?:--data(?:-raw)?|-d)\s+"([^"]*)"`)
		if matches := bodyReDouble.FindStringSubmatch(cmd); len(matches) > 1 {
			result.Body = matches[1]
		}
	}

	if result.Body != "" && result.Method == "GET" {
		result.Method = "POST"
	}

	if result.URL == "" {
		return nil, fmt.Errorf("no URL found in curl command")
	}

	return result, nil
}
