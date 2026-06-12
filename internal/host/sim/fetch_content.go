package sim

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	readability "github.com/go-shiori/go-readability"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	// maxTitleRunes 文件名中标题部分的最大长度
	maxTitleRunes = 60
	// minCorpusRunes 质检：正文最少非空白字符数，低于则警告"可能只抓到页面壳"
	minCorpusRunes = 500
	// minHanRatio 质检：汉字占比下限，低于则警告"可能抓到非正文内容"
	minHanRatio = 0.30
	// maxGarbledRatio 质检：U+FFFD 乱码字符占比上限，高于则警告"编码可能识别有误"
	maxGarbledRatio = 0.01
)

// windowsForbidden 是 Windows 文件/目录名的非法字符集（路径分隔符含在内）。
const windowsForbidden = `<>:"/\|?*`

// validateAuthorDirName 校验作者名可安全用作 personas/ 子目录名：
// 拒绝空名、"."/".."、路径分隔符、Windows 非法字符、控制字符、点结尾。
func validateAuthorDirName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("作者名不能为空")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("作者名不能是路径成分：%q", name)
	}
	if strings.HasSuffix(name, ".") {
		return fmt.Errorf("作者名不能以点结尾（Windows 目录名限制）：%q", name)
	}
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(windowsForbidden, r) {
			return fmt.Errorf("作者名含非法字符：%q", name)
		}
	}
	return nil
}

// sanitizeTitleForFile 把页面标题清洗成可用的文件名片段：
// 非法字符换空格、压缩连续空白、截断到 maxTitleRunes、去结尾点；空标题退化为 untitled。
func sanitizeTitleForFile(title string) string {
	var b strings.Builder
	for _, r := range title {
		if r < 0x20 || strings.ContainsRune(windowsForbidden, r) {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	cleaned := strings.Join(strings.Fields(b.String()), " ")
	if runes := []rune(cleaned); len(runes) > maxTitleRunes {
		cleaned = string(runes[:maxTitleRunes])
	}
	cleaned = strings.TrimRight(strings.TrimSpace(cleaned), ".")
	if cleaned == "" {
		return "untitled"
	}
	return cleaned
}

// corpusFileName 生成语料文件名：清洗后标题 + URL 短 hash + .txt。
// 短 hash 保证同 URL 重抓覆盖同名文件、异 URL 同标题不冲突。
func corpusFileName(title, rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return fmt.Sprintf("%s-%s.txt", sanitizeTitleForFile(title), hex.EncodeToString(sum[:])[:8])
}

// qualityWarnings 对提取后的正文做启发式质检，返回警告列表（不阻断落盘）。
// 统计均排除空白字符。
func qualityWarnings(text string) []string {
	var warns []string
	var total, han, garbled int
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if unicode.Is(unicode.Han, r) {
			han++
		}
		if r == utf8.RuneError {
			garbled++
		}
	}
	if total < minCorpusRunes {
		warns = append(warns, fmt.Sprintf("正文过短（%d 字符），可能只抓到页面壳", total))
	}
	if total > 0 {
		if ratio := float64(han) / float64(total); ratio < minHanRatio {
			warns = append(warns, fmt.Sprintf("中文占比仅 %.0f%%，可能抓到导航或非正文内容", ratio*100))
		}
		if ratio := float64(garbled) / float64(total); ratio > maxGarbledRatio {
			warns = append(warns, fmt.Sprintf("乱码字符占比 %.1f%%，编码可能识别有误", ratio*100))
		}
	}
	return warns
}

// normalizeText 规整正文：统一换行符、去行尾空白、压缩 2 个以上连续空行为 1 个，
// 保证落盘文件以单个换行结尾。
func normalizeText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, ln := range lines {
		ln = strings.TrimRight(ln, " \t")
		if strings.TrimSpace(ln) == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, ln)
	}
	joined := strings.TrimSpace(strings.Join(out, "\n"))
	if joined == "" {
		return ""
	}
	return joined + "\n"
}

// decodePlainText 解码 txt 直链内容：UTF-8 校验通过则直通，
// 否则按 GB18030（GBK 超集）解码。无法识别的字节会变成 U+FFFD，由质检兜底。
func decodePlainText(data []byte) (string, error) {
	if utf8.Valid(data) {
		return string(data), nil
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
	if err != nil {
		return "", fmt.Errorf("非 UTF-8 内容且 GB18030 解码失败: %w", err)
	}
	return string(decoded), nil
}

// extractArticle 从 HTML 字节流提取正文：charset.NewReader 按
// Content-Type 头/meta 标签/BOM 自动转码，再交 readability 提取。
// 标题为空时退化为页面 host。
func extractArticle(body []byte, contentType string, pageURL *url.URL) (title, text string, err error) {
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return "", "", fmt.Errorf("识别页面编码失败: %w", err)
	}
	article, err := readability.FromReader(reader, pageURL)
	if err != nil {
		return "", "", fmt.Errorf("正文提取失败: %w", err)
	}
	text = normalizeText(article.TextContent)
	if text == "" {
		return "", "", fmt.Errorf("未能从页面提取到正文")
	}
	title = strings.TrimSpace(article.Title)
	if title == "" {
		title = pageURL.Host
	}
	return title, text, nil
}
