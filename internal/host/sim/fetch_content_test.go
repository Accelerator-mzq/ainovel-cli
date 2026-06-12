package sim

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestValidateAuthorDirName(t *testing.T) {
	// ".hidden" 前导点但非 ".."：实现只拒结尾点，前导点合法
	valid := []string{"余华", "刘慈欣", "Stephen King", "天涯..客", ".hidden"}
	for _, name := range valid {
		if err := validateAuthorDirName(name); err != nil {
			t.Errorf("validateAuthorDirName(%q) = %v, want nil", name, err)
		}
	}
	// "CON"/"nul"/"com1" 为 Windows 保留设备名（大小写不敏感）；"CON.txt" 验证剥扩展名后仍保留；"a\x00b" 为控制字符
	invalid := []string{"", "  ", ".", "..", "a/b", `a\b`, "a?b", "a*b", `a"b`, "a<b", "尾点.", "CON", "nul", "com1", "CON.txt", "a\x00b"}
	for _, name := range invalid {
		if err := validateAuthorDirName(name); err == nil {
			t.Errorf("validateAuthorDirName(%q) = nil, want error", name)
		}
	}
}

func TestSanitizeTitleForFile(t *testing.T) {
	cases := []struct{ in, want string }{
		{`第1章：风雪夜<归人>?`, "第1章：风雪夜 归人"}, // 全角冒号合法，半角非法字符换空格后压缩
		{"", "untitled"},
		{"   ", "untitled"},
		{"结尾是点...", "结尾是点"}, // Windows 文件名不能以点结尾
		{"<<<>>>", "untitled"},  // 全非法字符标题清洗后为空，退化为 untitled
	}
	for _, c := range cases {
		if got := sanitizeTitleForFile(c.in); got != c.want {
			t.Errorf("sanitizeTitleForFile(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("长", 100)
	if got := sanitizeTitleForFile(long); len([]rune(got)) != 60 {
		t.Errorf("超长标题应截到 60 字符，got %d", len([]rune(got)))
	}
}

func TestCorpusFileName(t *testing.T) {
	a := corpusFileName("同名章节", "https://a.example.com/1")
	b := corpusFileName("同名章节", "https://b.example.com/1")
	if a == b {
		t.Fatalf("不同 URL 同标题不应同名: %q", a)
	}
	if again := corpusFileName("同名章节", "https://a.example.com/1"); again != a {
		t.Fatalf("同 URL 文件名应确定：%q vs %q", again, a)
	}
	if !strings.HasSuffix(a, ".txt") {
		t.Fatalf("应以 .txt 结尾: %q", a)
	}
}

func TestQualityWarnings(t *testing.T) {
	// 干净长中文：无警告
	clean := strings.Repeat("他抬起头看着远处的山，雪还在下，路灯把影子拉得很长。", 30)
	if w := qualityWarnings(clean); len(w) != 0 {
		t.Fatalf("干净语料不应有警告: %v", w)
	}
	// 过短
	if w := qualityWarnings("太短了"); len(w) == 0 {
		t.Fatal("过短文本应有警告")
	}
	// 中文占比低
	english := strings.Repeat("the quick brown fox jumps over the lazy dog ", 30)
	if w := qualityWarnings(english); !containsWarn(w, "中文占比") {
		t.Fatalf("英文文本应报中文占比警告: %v", w)
	}
	// 乱码超阈
	garbled := strings.Repeat("正常文字"+string('�'), 200)
	if w := qualityWarnings(garbled); !containsWarn(w, "乱码") {
		t.Fatalf("乱码文本应报乱码警告: %v", w)
	}
}

func containsWarn(warns []string, key string) bool {
	for _, w := range warns {
		if strings.Contains(w, key) {
			return true
		}
	}
	return false
}

func TestNormalizeText(t *testing.T) {
	in := "第一行  \r\n\r\n\r\n\r\n第二行\t\n"
	want := "第一行\n\n第二行\n"
	if got := normalizeText(in); got != want {
		t.Fatalf("normalizeText = %q, want %q", got, want)
	}
}

func TestDecodePlainText(t *testing.T) {
	utf8Text := "这是一段 UTF-8 中文文本。"
	if got, err := decodePlainText([]byte(utf8Text)); err != nil || got != utf8Text {
		t.Fatalf("UTF-8 直通失败: %q, %v", got, err)
	}
	gbk, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("这是一段 GBK 编码的中文文本。"))
	if err != nil {
		t.Fatalf("构造 GBK 测试数据失败: %v", err)
	}
	got, err := decodePlainText(gbk)
	if err != nil {
		t.Fatalf("GB18030 解码失败: %v", err)
	}
	if got != "这是一段 GBK 编码的中文文本。" {
		t.Fatalf("GB18030 解码结果 = %q", got)
	}
}

func TestExtractArticle(t *testing.T) {
	para := strings.Repeat("<p>他在镇上住了三十年，每天清晨沿着河堤走到渡口，看船来船往，这个习惯从未改变过。</p>", 12)
	html := "<html><head><title>渡口三十年</title></head><body><article>" + para + "</article></body></html>"
	u, _ := url.Parse("https://example.com/article/1")
	title, text, err := extractArticle([]byte(html), "text/html; charset=utf-8", u)
	if err != nil {
		t.Fatalf("extractArticle 失败: %v", err)
	}
	if !strings.Contains(title, "渡口三十年") {
		t.Errorf("title = %q, 应含页面标题", title)
	}
	if !strings.Contains(text, "他在镇上住了三十年") {
		t.Errorf("正文未提取到段落内容: %q", text[:min(120, len(text))])
	}

	// 无 <title> 标签时，标题应回退为页面 host
	noTitleHTML := "<html><head></head><body><article>" + para + "</article></body></html>"
	title2, _, err := extractArticle([]byte(noTitleHTML), "text/html; charset=utf-8", u)
	if err != nil {
		t.Fatalf("extractArticle（无 title）失败: %v", err)
	}
	if title2 != "example.com" {
		t.Errorf("空 title 应回退 pageURL.Host，got %q", title2)
	}
}
