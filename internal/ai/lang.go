package ai

import "github.com/abadojack/whatlanggo"

// isChineseLang 检测文本是否主要为中文
func isChineseLang(text string) bool {
	if len(text) == 0 {
		return true // 空文本视为中文，跳过翻译
	}
	info := whatlanggo.Detect(text)
	return info.Lang == whatlanggo.Cmn // Cmn = Chinese Mandarin
}
