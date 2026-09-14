package crawler

import (
	"bytes"
	"io"
	"log"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// isIdentityUTF8 判断该编码是否无需转换：nil、UTF-8，以及 x/net 对 UTF-8
// 返回的 Nop（"UTF-8 转 UTF-8" 恒等编码，htmlindex/DetermineEncoding 的历史约定）
func isIdentityUTF8(enc encoding.Encoding) bool {
	return enc == nil || enc == unicode.UTF8 || enc == encoding.Nop
}

// DecodeToUTF8 将抓取到的页面字节流解码为 UTF-8 字符串。
// 编码优先级：explicit 手动指定 > HTTP Content-Type 头 charset > BOM > 页面 meta 声明 > 默认 UTF-8。
// 已是 UTF-8 时原样返回；按识别出的编码解码失败（脏字节/半截字符）时也回退原文——
// 乱码不应导致整次抓取失败，识别不准时用户可手动指定 explicit 兜底
func DecodeToUTF8(data []byte, explicit, contentType string) string {
	enc := resolveEncoding(data, explicit, contentType)
	if isIdentityUTF8(enc) {
		return string(data)
	}
	decoded, err := enc.NewDecoder().Bytes(data)
	if err != nil {
		log.Printf("charset: decode %d bytes failed (%v), fallback to raw bytes", len(data), err)
		return string(data)
	}
	return string(decoded)
}

// DecodeToUTF8Reader 返回解码为 UTF-8 的流：识别为 UTF-8（绝大多数页面）时
// 零拷贝返回原字节 Reader；其他编码用 transform 流式转换。
// 编码识别优先级同 DecodeToUTF8。流式路径无法整体回退原文，无效字节按
// transform 默认替换为 U+FFFD，适用于正文可容忍少量替换符的流式消费方
func DecodeToUTF8Reader(data []byte, explicit, contentType string) io.Reader {
	enc := resolveEncoding(data, explicit, contentType)
	if isIdentityUTF8(enc) {
		return bytes.NewReader(data)
	}
	return transform.NewReader(bytes.NewReader(data), enc.NewDecoder())
}

// resolveEncoding 决定解码所用编码。手动指定优先：支持 gb2312/gbk/gb18030/big5/
// windows-1252 等 IANA 名称（htmlindex 映射，gb2312/gbk 均落到 GB18030 超集解码器）；
// 未指定或名称无效时用 x/net 的 DetermineEncoding 综合响应头与页面 meta 自动识别。
// DetermineEncoding 按 WHATWG 把无声明页面兜底成 windows-1252，会误转合法 UTF-8 中文页：
// 无明确声明（certain=false）且数据是合法 UTF-8 时返回 nil（原样直通，与旧行为一致）
func resolveEncoding(data []byte, explicit, contentType string) encoding.Encoding {
	if name := strings.TrimSpace(explicit); name != "" {
		if enc, err := htmlindex.Get(name); err == nil {
			return enc
		}
		log.Printf("charset: unknown encoding %q, falling back to auto-detect", explicit)
	}
	// 新版 x/net 返回 (编码, 名称, 是否确定)；certain=false 覆盖 meta 嗅探与 WHATWG 兜底两类
	enc, _, certain := charset.DetermineEncoding(data, contentType)
	if !certain && utf8.Valid(data) {
		return nil
	}
	return enc
}
