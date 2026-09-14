package crawler

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// encodeGB18030 把 UTF-8 文本编码为 GB 系字节流（GB2312/GBK 是 GB18030 的子集，模拟老站响应）
func encodeGB18030(t *testing.T, page string) []byte {
	t.Helper()
	raw, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(page))
	if err != nil {
		t.Fatalf("encode page to GB18030 failed: %v", err)
	}
	return raw
}

// meta charset 声明 gb2312 → 自动识别转码
func TestDecodeToUTF8AutoMetaCharset(t *testing.T) {
	page := `<html><head><meta charset="gb2312"><title>考试院通知</title></head><body>招生公告</body></html>`
	got := DecodeToUTF8(encodeGB18030(t, page), "", "")
	if !strings.Contains(got, "考试院通知") || !strings.Contains(got, "招生公告") {
		t.Errorf("DecodeToUTF8() = %q, want 解码出 GB2312 中文", got)
	}
}

// http-equiv 形式的 meta 声明 gbk → 自动识别转码
func TestDecodeToUTF8AutoMetaHTTPEquiv(t *testing.T) {
	page := `<html><head><meta http-equiv="Content-Type" content="text/html; charset=gbk"><title>通知列表</title></head></html>`
	got := DecodeToUTF8(encodeGB18030(t, page), "", "")
	if !strings.Contains(got, "通知列表") {
		t.Errorf("DecodeToUTF8() = %q, want 解码出 GBK 中文", got)
	}
}

// HTTP Content-Type 头带 charset、页面无 meta → 靠响应头识别
func TestDecodeToUTF8AutoHeader(t *testing.T) {
	page := `<html><head><title>头部声明编码</title></head></html>`
	got := DecodeToUTF8(encodeGB18030(t, page), "", "text/html; charset=gbk")
	if !strings.Contains(got, "头部声明编码") {
		t.Errorf("DecodeToUTF8() = %q, want 按 header charset 解码", got)
	}
}

// 无任何声明 → 按默认 UTF-8 原样返回（乱码），手动指定 gbk 才能解
func TestDecodeToUTF8ExplicitFallbackWhenUndeclared(t *testing.T) {
	page := `<html><head><title>无声明编码页</title></head></html>`
	raw := encodeGB18030(t, page)
	if got := DecodeToUTF8(raw, "", ""); strings.Contains(got, "无声明编码页") {
		t.Errorf("无声明时应按 UTF-8 原样返回，实际解出了中文")
	}
	got := DecodeToUTF8(raw, "gbk", "")
	if !strings.Contains(got, "无声明编码页") {
		t.Errorf("手动 gbk 应解码成功，got %q", got)
	}
}

// 手动指定覆盖页面 meta 谎报（meta 写 utf-8 实际是 GBK）
func TestDecodeToUTF8ExplicitOverridesMeta(t *testing.T) {
	page := `<html><head><meta charset="utf-8"><title>谎报编码页</title></head></html>`
	raw := encodeGB18030(t, page)
	got := DecodeToUTF8(raw, "gbk", "")
	if !strings.Contains(got, "谎报编码页") {
		t.Errorf("手动编码应优先于 meta 声明，got %q", got)
	}
}

// UTF-8 内容原样返回（含手动指定 utf-8 与不指定两种路径）
func TestDecodeToUTF8Passthrough(t *testing.T) {
	page := "<html><body>中文内容</body></html>"
	if got := DecodeToUTF8([]byte(page), "", "text/html; charset=utf-8"); got != page {
		t.Errorf("UTF-8 页面应原样返回，got %q", got)
	}
	if got := DecodeToUTF8([]byte(page), "utf-8", ""); got != page {
		t.Errorf("显式 utf-8 应原样返回，got %q", got)
	}
}

// 未知编码名回退自动识别
func TestDecodeToUTF8UnknownExplicitFallsBack(t *testing.T) {
	page := `<html><head><meta charset="gb2312"><title>回退识别页</title></head></html>`
	got := DecodeToUTF8(encodeGB18030(t, page), "不存在的编码", "")
	if !strings.Contains(got, "回退识别页") {
		t.Errorf("无效编码名应回退自动识别，got %q", got)
	}
}

// Reader 版与字符串版语义一致：UTF-8 直通、GBK 流式转换、显式编码覆盖
func TestDecodeToUTF8Reader(t *testing.T) {
	// 无声明的 UTF-8 页面（合法 UTF-8 短路）→ 零拷贝直通
	if r, ok := DecodeToUTF8Reader([]byte("<html><body>中文内容</body></html>"), "", "").(*bytes.Reader); !ok {
		t.Errorf("无声明 UTF-8 页面应零拷贝返回 bytes.Reader，实际 %T", r)
	}

	// 显式 header 声明 utf-8：x/net 返回 htmlEncoding 包装（含换行/NUL 规范化），
	// 走 transform 路径，验证内容等价而非类型
	out, err := io.ReadAll(DecodeToUTF8Reader([]byte("<html><body>中文内容</body></html>"), "", "text/html; charset=utf-8"))
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(out) != "<html><body>中文内容</body></html>" {
		t.Errorf("显式 utf-8 解码内容应等价，got %q", out)
	}

	gbkPage := `<html><head><meta charset="gbk"><title>流式转码页</title></head></html>`
	out, err = io.ReadAll(DecodeToUTF8Reader(encodeGB18030(t, gbkPage), "", ""))
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if !strings.Contains(string(out), "流式转码页") {
		t.Errorf("GBK 流式解码失败，got %q", out)
	}

	noDecl := `<html><head><title>无声明流式</title></head></html>`
	out, err = io.ReadAll(DecodeToUTF8Reader(encodeGB18030(t, noDecl), "gbk", ""))
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if !strings.Contains(string(out), "无声明流式") {
		t.Errorf("显式编码流式解码失败，got %q", out)
	}
}

// WHATWG 兜底回归：DetermineEncoding 把无声明页面判为 windows-1252，
// 合法 UTF-8 中文页必须原样直通而非被转成 mojibake（字符串版同样受此保护）
func TestDecodeNoDeclarationUTF8Passthrough(t *testing.T) {
	page := "<html><head><title>无声明 UTF-8 中文页</title></head><body>正文内容</body></html>"
	if got := DecodeToUTF8([]byte(page), "", ""); got != page {
		t.Errorf("无声明 UTF-8 页面应原样返回，got %q", got)
	}
}
