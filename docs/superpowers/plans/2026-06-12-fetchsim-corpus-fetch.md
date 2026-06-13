# /fetchsim 网络语料获取 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `/fetchsim <作者名> <url>...` 命令：抓取静态网页/txt 直链，readability 提取正文，清洗为 UTF-8 语料落盘 `simulate/personas/<作者名>/`，输出质检摘要，提示用户检查后自行跑 `/simulate`。

**Architecture:** 复用 `internal/host/sim` 包的事件通道模式（`RunFetch` 返回 `<-chan Event`，同 `Run`/`RunImport`）；不调 LLM、不动 Store。纯函数（命名/质检/解码）与流水线（HTTP/编排）分文件，前者无网络单测，后者走 `httptest`。TUI 完全复用 `simulationState` 模态框，新增 `doneHint` 字段定制完成提示。

**Tech Stack:** Go 1.25；新增直接依赖 `github.com/go-shiori/go-readability`（正文提取）、`golang.org/x/net/html/charset`（HTML 编码探测）、`golang.org/x/text/encoding/simplifiedchinese`（GB18030 解码）。

**Spec:** `docs/superpowers/specs/2026-06-12-fetchsim-corpus-fetch-design.md`

**与 spec 的签名细化：** spec 写的 `RunFetch(ctx, root, author, urls)` 落地为 `RunFetch(ctx, FetchOptions{SourceDir, Author, URLs, ...})`，与现有 `Run(ctx, deps, Options)` 的参数风格一致；`FetchOptions` 额外带 `Client`/`MaxBodyBytes` 两个测试注入位（零值即生产默认）。

**设计决策备忘（执行时不要"改进"掉）：**
- 单条 URL 抓取/提取失败：事件 `Err` 字段必须传 nil，失败原因放 `Message`（"✗ ..."）。因为 TUI 的 `simulationState.appendEvent` 一旦收到非 nil Err 就把整个面板置为失败态，部分失败仍应以成功收尾。
- URL scheme 校验（仅 http/https）在 `RunFetch` 返回前同步做完，属"用法错误"，TUI 显示启动失败；网络/提取失败才是逐条独立处理。
- 质检警告**不阻断落盘**，只进摘要。

---

### Task 1: 分支与依赖引入

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: 创建特性分支**

```bash
cd /d/ClaudeProject/ainovel-cli
git checkout -b feat/fetchsim-corpus
```

- [ ] **Step 2: 引入依赖**

```bash
go get github.com/go-shiori/go-readability@latest
go get golang.org/x/net/html/charset
go get golang.org/x/text/encoding/simplifiedchinese
```

- [ ] **Step 3: 验证构建**

Run: `go build ./...`
Expected: 无输出（成功）。注意 `golang.org/x/net`、`golang.org/x/text` 此时仍可能标 `// indirect`，在 Task 2/3 的代码 import 后跑 `go mod tidy` 会转正。

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "build: 引入 go-readability 与编码处理依赖"
```

---

### Task 2: 内容处理纯函数（fetch_content.go）

**Files:**
- Create: `internal/host/sim/fetch_content.go`
- Test: `internal/host/sim/fetch_content_test.go`

- [ ] **Step 1: 写失败测试**

创建 `internal/host/sim/fetch_content_test.go`：

```go
package sim

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestValidateAuthorDirName(t *testing.T) {
	valid := []string{"余华", "刘慈欣", "Stephen King", "天涯..客"}
	for _, name := range valid {
		if err := validateAuthorDirName(name); err != nil {
			t.Errorf("validateAuthorDirName(%q) = %v, want nil", name, err)
		}
	}
	invalid := []string{"", "  ", ".", "..", "a/b", `a\b`, "a?b", "a*b", `a"b`, "a<b", "尾点."}
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
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/host/sim/ -run 'TestValidateAuthorDirName|TestSanitizeTitleForFile|TestCorpusFileName|TestQualityWarnings|TestNormalizeText|TestDecodePlainText|TestExtractArticle' -v`
Expected: 编译失败，`undefined: validateAuthorDirName` 等。

- [ ] **Step 3: 实现 fetch_content.go**

创建 `internal/host/sim/fetch_content.go`：

```go
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/host/sim/ -run 'TestValidateAuthorDirName|TestSanitizeTitleForFile|TestCorpusFileName|TestQualityWarnings|TestNormalizeText|TestDecodePlainText|TestExtractArticle' -v`
Expected: 全部 PASS。若 `TestExtractArticle` 因 readability 对短页面提取为空而失败，把测试夹具的段落重复次数从 12 提到 30 再跑。

注意：`readability.FromReader` 在新版的签名是 `FromReader(input io.Reader, pageURL *url.URL) (Article, error)`；若拉到的版本是旧 API（第二参数为 string），改为传 `pageURL.String()` 即可，编译器会直接报出来。

- [ ] **Step 5: go mod tidy 并提交**

```bash
go mod tidy
git add internal/host/sim/fetch_content.go internal/host/sim/fetch_content_test.go go.mod go.sum
git commit -m "feat(sim): 语料抓取内容处理纯函数（命名/质检/解码/提取）"
```

---

### Task 3: RunFetch 抓取流水线（fetch.go）

**Files:**
- Create: `internal/host/sim/fetch.go`
- Modify: `internal/host/sim/types.go`（加 StageFetch）
- Test: `internal/host/sim/fetch_test.go`

- [ ] **Step 1: 写失败测试**

创建 `internal/host/sim/fetch_test.go`：

```go
package sim

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// drainFetch 启动 RunFetch 并收完所有事件。
func drainFetch(t *testing.T, opts FetchOptions) []Event {
	t.Helper()
	ch, err := RunFetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunFetch: %v", err)
	}
	var events []Event
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) == 0 {
		t.Fatal("没有收到任何事件")
	}
	return events
}

func lastEvent(events []Event) Event { return events[len(events)-1] }

// fixtureHTML 生成一个 readability 可稳定提取的中文文章页。
func fixtureHTML(title string) string {
	para := strings.Repeat("<p>他在镇上住了三十年，每天清晨沿着河堤走到渡口，看船来船往，这个习惯从未改变过，连下雪天也不例外。</p>", 30)
	return "<html><head><title>" + title + "</title></head><body><article>" + para + "</article></body></html>"
}

func TestRunFetchHTMLUTF8(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixtureHTML("渡口三十年")))
	}))
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL + "/article/1"}})

	if last := lastEvent(events); last.Stage != StageDone {
		t.Fatalf("last stage = %s, want %s（events: %+v）", last.Stage, StageDone, events)
	}
	dir := filepath.Join(root, "personas", "测试作者")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("应落盘 1 个语料文件: %v, %v", entries, err)
	}
	if !strings.Contains(entries[0].Name(), "渡口三十年") || !strings.HasSuffix(entries[0].Name(), ".txt") {
		t.Fatalf("文件名应含标题且为 .txt: %q", entries[0].Name())
	}
	data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !strings.Contains(string(data), "他在镇上住了三十年") {
		t.Fatal("落盘内容应含正文")
	}
}

func TestRunFetchHTMLGBK(t *testing.T) {
	gbkBody, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(fixtureHTML("雪夜渡口")))
	if err != nil {
		t.Fatalf("构造 GBK 页面失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=gbk")
		_, _ = w.Write(gbkBody)
	}))
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL}})
	if last := lastEvent(events); last.Stage != StageDone {
		t.Fatalf("last stage = %s, want done", last.Stage)
	}
	dir := filepath.Join(root, "personas", "测试作者")
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("应落盘 1 个文件，got %d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !strings.Contains(string(data), "他在镇上住了三十年") {
		t.Fatal("GBK 页面应正确解码出中文正文")
	}
}

func TestRunFetchTxtDirectLink(t *testing.T) {
	novel := strings.Repeat("江水东去，他守着这条渡船过了半生，岸边的柳树绿了又黄。\n", 40)
	gbkTxt, _ := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(novel))
	mux := http.NewServeMux()
	mux.HandleFunc("/utf8.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(novel))
	})
	mux.HandleFunc("/gbk.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream") // 故意不给 text/plain，靠 .txt 后缀分流
		_, _ = w.Write(gbkTxt)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{
		SourceDir: root, Author: "测试作者",
		URLs: []string{srv.URL + "/utf8.txt", srv.URL + "/gbk.txt"},
	})
	if last := lastEvent(events); last.Stage != StageDone {
		t.Fatalf("last stage = %s, want done（%+v）", last.Stage, events)
	}
	dir := filepath.Join(root, "personas", "测试作者")
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("应落盘 2 个文件，got %d", len(entries))
	}
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if !strings.Contains(string(data), "江水东去") {
			t.Fatalf("%s 内容解码错误", e.Name())
		}
	}
}

func TestRunFetchPartialFailureStillDone(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixtureHTML("正常页")))
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{
		SourceDir: root, Author: "测试作者",
		URLs: []string{srv.URL + "/ok", srv.URL + "/missing", srv.URL + "/pdf"},
	})
	last := lastEvent(events)
	if last.Stage != StageDone {
		t.Fatalf("部分失败仍应 done 收尾，got %s", last.Stage)
	}
	if !strings.Contains(last.Message, "1/3") {
		t.Fatalf("摘要应含成功比例 1/3：%q", last.Message)
	}
	// 失败明细事件 Err 必须为 nil（否则 TUI 把整个面板置为失败态）
	for _, ev := range events {
		if ev.Stage == StageFetch && ev.Err != nil {
			t.Fatalf("逐条失败应只进 Message 不设 Err：%+v", ev)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(root, "personas", "测试作者"))
	if len(entries) != 1 {
		t.Fatalf("只应落盘成功的 1 个文件，got %d", len(entries))
	}
}

func TestRunFetchAllFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL}})
	if last := lastEvent(events); last.Stage != StageError {
		t.Fatalf("全部失败应以 error 收尾，got %s", last.Stage)
	}
}

func TestRunFetchOversizeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("a", 4096)))
	}))
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{
		SourceDir: root, Author: "测试作者",
		URLs:         []string{srv.URL + "/big.txt"},
		MaxBodyBytes: 1024, // 测试注入小上限
	})
	if last := lastEvent(events); last.Stage != StageError {
		t.Fatalf("超限应整批失败（唯一 URL），got %s", last.Stage)
	}
}

func TestRunFetchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{
		SourceDir: root, Author: "测试作者",
		URLs:   []string{srv.URL},
		Client: &http.Client{Timeout: 20 * time.Millisecond},
	})
	if last := lastEvent(events); last.Stage != StageError {
		t.Fatalf("超时应失败，got %s", last.Stage)
	}
}

func TestRunFetchRefetchOverwritesSameFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixtureHTML("同一篇")))
	}))
	defer srv.Close()

	root := t.TempDir()
	opts := FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL + "/same"}}
	drainFetch(t, opts)
	drainFetch(t, opts) // 重抓同一 URL
	entries, _ := os.ReadDir(filepath.Join(root, "personas", "测试作者"))
	if len(entries) != 1 {
		t.Fatalf("同 URL 重抓应覆盖同名文件，got %d 个文件", len(entries))
	}
}

func TestRunFetchFollowsRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixtureHTML("重定向后的页面")))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL + "/old"}})
	if last := lastEvent(events); last.Stage != StageDone {
		t.Fatalf("重定向应被跟随并成功，got %s", last.Stage)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "personas", "测试作者"))
	if len(entries) != 1 {
		t.Fatalf("应落盘 1 个文件，got %d", len(entries))
	}
}

func TestRunFetchRejectsBadInput(t *testing.T) {
	root := t.TempDir()
	cases := []FetchOptions{
		{SourceDir: root, Author: "../逃逸", URLs: []string{"https://example.com"}}, // 路径穿越
		{SourceDir: root, Author: "正常", URLs: []string{"ftp://example.com/a.txt"}}, // 非 http(s)
		{SourceDir: root, Author: "正常", URLs: nil},                                 // 无 URL
		{SourceDir: "", Author: "正常", URLs: []string{"https://example.com"}},       // 无根目录
	}
	for i, opts := range cases {
		if _, err := RunFetch(context.Background(), opts); err == nil {
			t.Errorf("case %d 应同步返回错误", i)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/host/sim/ -run 'TestRunFetch' -v`
Expected: 编译失败，`undefined: RunFetch`、`undefined: FetchOptions`、`undefined: StageFetch`。

- [ ] **Step 3: types.go 加 StageFetch**

在 `internal/host/sim/types.go` 的 const 块中 `StageImport` 后加一行：

```go
	StageFetch   Stage = "fetch"
```

- [ ] **Step 4: 实现 fetch.go**

创建 `internal/host/sim/fetch.go`：

```go
package sim

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	// fetchTimeout 单条 URL 的整体请求超时
	fetchTimeout = 30 * time.Second
	// fetchMaxBodyBytes 响应体大小上限（20MB），超限该条报错跳过
	fetchMaxBodyBytes = int64(20 << 20)
	// fetchUserAgent 模拟常见浏览器 UA——默认 Go UA 会被很多站点直接 403
	fetchUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

// FetchOptions 是 RunFetch 的参数。Client/MaxBodyBytes 为测试注入位，
// 零值即生产默认（30s 超时客户端 / 20MB 上限）。
type FetchOptions struct {
	SourceDir    string   // simulate 根目录（绝对路径，由 Host 解析）
	Author       string   // 作者名，将作为 personas/ 子目录名
	URLs         []string // 待抓取 URL，仅支持 http/https
	Client       *http.Client
	MaxBodyBytes int64
}

// fetchOutcome 是单条 URL 的处理结果。
type fetchOutcome struct {
	fileName string
	chars    int
	warnings []string
	err      error
}

// RunFetch 抓取网络语料落盘 simulate/personas/<作者>/，返回事件通道。
// 用法级错误（参数缺失、作者名非法、URL scheme 不支持）同步返回；
// 网络/提取失败逐条独立处理，单条失败不中断其余。
// 不调 LLM、不动 Store——落盘后由用户检查质量再自行运行 /simulate。
func RunFetch(ctx context.Context, opts FetchOptions) (<-chan Event, error) {
	if strings.TrimSpace(opts.SourceDir) == "" {
		return nil, fmt.Errorf("source dir is required")
	}
	if err := validateAuthorDirName(opts.Author); err != nil {
		return nil, err
	}
	if len(opts.URLs) == 0 {
		return nil, fmt.Errorf("至少需要一条 URL")
	}
	parsed := make([]*url.URL, 0, len(opts.URLs))
	for _, raw := range opts.URLs {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("仅支持 http/https URL：%s", raw)
		}
		parsed = append(parsed, u)
	}

	author := strings.TrimSpace(opts.Author)
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = fetchMaxBodyBytes
	}

	events := make(chan Event, 32)
	go func() {
		defer close(events)
		emit := func(stage Stage, current, total int, msg string, err error) {
			ev := Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg, Err: err}
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		}

		destDir := filepath.Join(opts.SourceDir, personasDirName, author)
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			emit(StageError, 0, 0, "创建语料目录失败", err)
			return
		}

		okCount := 0
		for i, u := range parsed {
			if err := ctx.Err(); err != nil {
				emit(StageError, i, len(parsed), "用户取消语料抓取", err)
				return
			}
			emit(StageFetch, i+1, len(parsed), fmt.Sprintf("抓取 %d/%d：%s", i+1, len(parsed), u.String()), nil)
			out := fetchOne(ctx, client, u, destDir, maxBody)
			if out.err != nil {
				// 注意：单条失败 Err 必须传 nil，原因进 Message。
				// TUI 的 appendEvent 收到非 nil Err 会把整个面板置为失败态，
				// 而部分失败仍应以成功收尾。
				emit(StageFetch, i+1, len(parsed), fmt.Sprintf("✗ %s：%v", u.String(), out.err), nil)
				continue
			}
			okCount++
			msg := fmt.Sprintf("✓ %s（%d 字符）", out.fileName, out.chars)
			if len(out.warnings) > 0 {
				msg += "　⚠ " + strings.Join(out.warnings, "；")
			}
			emit(StageFetch, i+1, len(parsed), msg, nil)
		}

		if okCount == 0 {
			emit(StageError, len(parsed), len(parsed), "全部 URL 抓取失败", fmt.Errorf("no corpus fetched"))
			return
		}
		emit(StageDone, len(parsed), len(parsed), fmt.Sprintf(
			"语料抓取完成：成功 %d/%d，落盘 simulate/%s/%s/；请检查语料质量后运行 /simulate 生成画像",
			okCount, len(parsed), personasDirName, author), nil)
	}()
	return events, nil
}

// fetchOne 处理单条 URL：抓取 → 按 Content-Type 分流解码/提取 → 质检 → 落盘。
func fetchOne(ctx context.Context, client *http.Client, u *url.URL, destDir string, maxBody int64) fetchOutcome {
	var out fetchOutcome
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		out.err = err
		return out
	}
	req.Header.Set("User-Agent", fetchUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		out.err = fmt.Errorf("请求失败: %w", err)
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.err = fmt.Errorf("HTTP %d", resp.StatusCode)
		return out
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		out.err = fmt.Errorf("读取响应失败: %w", err)
		return out
	}
	if int64(len(body)) > maxBody {
		out.err = fmt.Errorf("响应超过 %dKB 上限", maxBody>>10)
		return out
	}

	contentType := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)
	var title, text string
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		title, text, err = extractArticle(body, contentType, u)
	case mediaType == "text/plain" || strings.HasSuffix(strings.ToLower(u.Path), ".txt"):
		text, err = decodePlainText(body)
		if err == nil {
			text = normalizeText(text)
			if text == "" {
				err = fmt.Errorf("txt 内容为空")
			}
			title = strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
		}
	default:
		err = fmt.Errorf("不支持的内容类型 %q（仅支持 HTML 页面与 txt 文本）", mediaType)
	}
	if err != nil {
		out.err = err
		return out
	}

	out.fileName = corpusFileName(title, u.String())
	if err := os.WriteFile(filepath.Join(destDir, out.fileName), []byte(text), 0o644); err != nil {
		out.err = fmt.Errorf("写入语料文件失败: %w", err)
		return out
	}
	out.chars = len([]rune(text))
	out.warnings = qualityWarnings(text)
	return out
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/host/sim/ -run 'TestRunFetch' -v`
Expected: 全部 PASS。

- [ ] **Step 6: 跑 sim 包全量测试（防回归）**

Run: `go test ./internal/host/sim/`
Expected: ok。

- [ ] **Step 7: Commit**

```bash
git add internal/host/sim/fetch.go internal/host/sim/fetch_test.go internal/host/sim/types.go
git commit -m "feat(sim): RunFetch 网络语料抓取流水线（httptest 全覆盖）"
```

---

### Task 4: Host.FetchSimulationCorpus

**Files:**
- Modify: `internal/host/host.go`（在 `ImportSimulationProfile` 之后，约 980 行处）

- [ ] **Step 1: 添加 Host 方法**

在 `internal/host/host.go` 的 `ImportSimulationProfile` 方法后插入：

```go
// FetchSimulationCorpus 从网络抓取作者语料，落盘 simulate/personas/<作者>/。
// 只落语料文件不生成画像——用户检查质量后自行运行 /simulate。
func (h *Host) FetchSimulationCorpus(ctx context.Context, author string, urls []string) (<-chan sim.Event, error) {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return nil, fmt.Errorf("coordinator 运行中，请先暂停后再抓取语料")
	}
	h.mu.Unlock()

	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working dir: %w", err)
	}
	return sim.RunFetch(ctx, sim.FetchOptions{
		SourceDir: filepath.Join(wd, "simulate"),
		Author:    author,
		URLs:      urls,
	})
}
```

- [ ] **Step 2: 构建验证**

Run: `go build ./...`
Expected: 成功。

- [ ] **Step 3: Commit**

```bash
git add internal/host/host.go
git commit -m "feat(host): FetchSimulationCorpus 封装（空闲门禁 + simulate 目录解析）"
```

---

### Task 5: TUI 命令注册与模态框

**Files:**
- Modify: `internal/entry/tui/simulation.go`（加 doneHint 字段 + startFetchSim）
- Modify: `internal/entry/tui/commands.go`（注册 fetchsim，插在 importsim 条目后，约 164 行处）
- Test: `internal/entry/tui/commands_simulation_test.go`

- [ ] **Step 1: 扩展注册测试（先改测试，确认失败）**

修改 `internal/entry/tui/commands_simulation_test.go`：

1. `TestSimulationCommandsAreRegisteredAndNeedIdle` 中的列表改为：

```go
	for _, name := range []string{"simulate", "importsim", "fetchsim"} {
```

2. 同函数的 palette 断言改为：

```go
	if !hasPaletteItem(items, "simulate") || !hasPaletteItem(items, "importsim") || !hasPaletteItem(items, "fetchsim") {
```

3. `TestSimulationCommandsAreBlockedWhileRunning` 的单命令调用改为循环：

```go
func TestSimulationCommandsAreBlockedWhileRunning(t *testing.T) {
	for _, name := range []string{"simulate", "fetchsim"} {
		m := Model{snapshot: host.UISnapshot{IsRunning: true}, eventIndex: map[string]int{}}
		next, _ := m.handleSlashCommand(slashCommand{name: name})
		got := next.(Model)
		if len(got.events) != 1 || got.events[0].Category != "ERROR" {
			t.Fatalf("/%s: expected NeedsIdle to emit one error, got %+v", name, got.events)
		}
		if got.simulator != nil {
			t.Fatalf("/%s: modal should not start while runtime is running", name)
		}
	}
}
```

Run: `go test ./internal/entry/tui/ -run 'TestSimulationCommands' -v`
Expected: FAIL，`expected /fetchsim command to be registered`。

- [ ] **Step 2: simulation.go 加 doneHint 与 startFetchSim**

修改 `internal/entry/tui/simulation.go`：

1. `simulationState` 结构体加字段（在 `viewport viewport.Model` 之前）：

```go
	// doneHint 成功完成时的底部提示；为空用默认文案（画像生成场景）
	doneHint string
```

2. `refresh` 方法的成功分支（`default:` 中 `b.WriteString(okStyle.Render("仿写画像已就绪，后续 Agent 会从 novel_context 读取"))`）改为：

```go
	default:
		hint := s.doneHint
		if hint == "" {
			hint = "仿写画像已就绪，后续 Agent 会从 novel_context 读取"
		}
		b.WriteString(okStyle.Render(hint))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc 关闭面板"))
```

（失败分支同理已有 "仿写画像处理失败" 文案，保持不动——抓取失败时该文案语义上仍可接受，YAGNI。）

3. 文件末尾加启动函数：

```go
func startFetchSim(rt *host.Host, reqID int, args []string, width, height int) (*simulationState, tea.Cmd, error) {
	if len(args) < 2 {
		return nil, nil, fmt.Errorf("用法：/fetchsim <作者名> <url> [url...]")
	}
	author, urls := args[0], args[1:]
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := rt.FetchSimulationCorpus(ctx, author, urls)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	state := newSimulationState(reqID, "抓取仿写语料",
		fmt.Sprintf("作者「%s」· %d 条 URL", author, len(urls)), width, height, cancel)
	state.doneHint = fmt.Sprintf("语料已落盘 simulate/personas/%s/，请检查质量后运行 /simulate 生成画像", author)
	return state, listenSimulationEvent(reqID, ch), nil
}
```

- [ ] **Step 3: commands.go 注册 fetchsim**

在 `internal/entry/tui/commands.go` 的 `importsim` 条目（约 164 行 `},` ）之后插入：

```go
		{
			Name:        "fetchsim",
			Group:       "writing",
			Usage:       "/fetchsim <作者名> <url> [url...]",
			Description: "抓取网页/txt 语料落盘 personas/<作者>/（不自动生成画像）",
			NeedsIdle:   true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				m.simSeq++
				state, listenCmd, err := startFetchSim(m.runtime, m.simSeq, args, m.width, m.height)
				if err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "语料抓取启动失败：" + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.simulator = state
				m.textarea.Blur()
				return m, listenCmd
			},
		},
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/entry/tui/ -run 'TestSimulationCommands' -v`
Expected: PASS。

- [ ] **Step 5: tui 包全量测试 + 构建**

Run: `go test ./internal/entry/tui/ && go build ./...`
Expected: ok。

- [ ] **Step 6: Commit**

```bash
git add internal/entry/tui/simulation.go internal/entry/tui/commands.go internal/entry/tui/commands_simulation_test.go
git commit -m "feat(tui): /fetchsim 命令注册与抓取进度模态框"
```

---

### Task 6: 文档更新

**Files:**
- Modify: `docs/user-guide.md`（命令表约 163 行 importsim 行后；小节约 213 行 /importsim 描述后）
- Modify: `README.md`（命令列表，先 grep `importsim` 定位）

- [ ] **Step 1: user-guide.md 命令表加行**

在 `docs/user-guide.md` 命令表 `/importsim` 行之后加：

```markdown
| `/fetchsim` | `/fetchsim <作者名> <url>...` | 仅空闲 | 抓取网页/txt 语料落盘 `simulate/personas/<作者>/` |
```

- [ ] **Step 2: user-guide.md 加小节**

在 `### /simulate 与 /importsim — 仿写画像` 小节末尾（约 213 行 `/importsim` 说明后）追加：

```markdown
### /fetchsim — 从网络抓取作者语料

不想手动找文件？`/fetchsim <作者名> <url> [url...]` 直接抓网页落语料：

```
/fetchsim 某作者 https://example.com/essay/1 https://example.com/essay/2
```

- 支持**静态 HTML 页**（自动提取正文、处理 GBK/UTF-8 编码）和 **txt 直链**；不支持需要 JS 渲染或登录的站点（起点、晋江等主站章节页抓不到正文）。
- 落盘到 `simulate/personas/<作者名>/`，每条 URL 一个 `.txt` 文件；同一 URL 重抓会覆盖旧文件。
- 抓完**不会**自动生成画像：摘要里有每条 URL 的提取字数和质量警告（正文过短/中文占比低/疑似乱码），请先打开文件确认语料干净，再运行 `/simulate`。
- **版权提示**：请只抓取你有权使用的公开内容（作者公开发布的免费章节、公版作品等），URL 的选择责任在用户。
```

- [ ] **Step 3: README.md 同步**

README 无命令表，是散文小节。在 `## 仿写画像` 小节中、`/importsim` 说明段（约 293 行 `...不复制原文表达或专有设定。`）之后追加一段：

```markdown
语料也可以直接从网上抓：`/fetchsim <作者名> <url> [url...]` 抓取静态网页（自动提取正文、处理 GBK/UTF-8）或 txt 直链，落盘到 `simulate/personas/<作者名>/`。不支持需 JS 渲染或登录的站点（起点、晋江等主站章节页）。抓完不会自动生成画像——请按摘要里的质量警告检查语料后再运行 `/simulate`。版权提示：仅抓取你有权使用的公开内容，URL 选择责任在用户。
```

- [ ] **Step 4: Commit**

```bash
git add docs/user-guide.md README.md
git commit -m "docs: /fetchsim 使用说明与版权提示"
```

---

### Task 7: 全量验证与真实 URL 冒烟

**Files:**
- Create: `internal/host/sim/fetch_e2e_test.go`（build tag 隔离，CI 不跑）

- [ ] **Step 1: 全量测试 + vet**

```bash
go test ./... && go vet ./...
```

Expected: 全部 ok。

- [ ] **Step 2: 写真实 URL 冒烟测试**

创建 `internal/host/sim/fetch_e2e_test.go`：

```go
//go:build e2e

package sim

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRunFetchRealURL 对真实公开静态页冒烟（维基文库公版文本）。
// 仅手动运行：go test -tags e2e -run TestRunFetchRealURL ./internal/host/sim/ -v
func TestRunFetchRealURL(t *testing.T) {
	root := t.TempDir()
	ch, err := RunFetch(context.Background(), FetchOptions{
		SourceDir: root,
		Author:    "冒烟测试",
		URLs:      []string{"https://zh.wikisource.org/wiki/%E5%AD%94%E4%B9%99%E5%B7%B1"},
	})
	if err != nil {
		t.Fatalf("RunFetch: %v", err)
	}
	var events []Event
	for ev := range ch {
		events = append(events, ev)
		t.Logf("[%s %d/%d] %s err=%v", ev.Stage, ev.Current, ev.Total, ev.Message, ev.Err)
	}
	last := events[len(events)-1]
	if last.Stage != StageDone {
		t.Fatalf("last stage = %s, want done", last.Stage)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "personas", "冒烟测试"))
	if len(entries) != 1 {
		t.Fatalf("应落盘 1 个文件，got %d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(root, "personas", "冒烟测试", entries[0].Name()))
	t.Logf("落盘 %s（%d bytes），前 200 字节：%s", entries[0].Name(), len(data), data[:min(200, len(data))])
}
```

- [ ] **Step 3: 跑冒烟并留证据**

```bash
go test -tags e2e -run TestRunFetchRealURL ./internal/host/sim/ -v 2>&1 | tee .smoke-fetchsim/e2e-real-url.log
```

（先 `mkdir -p .smoke-fetchsim`。）Expected: PASS，日志含落盘文件名、字数、StageDone 摘要。`.smoke-fetchsim/` 不提交，仅作为 PR 描述里的证据引用。若维基文库被墙/超时，换 `https://www.ruanyifeng.com/blog/` 任一文章页等可达静态页，并在日志中注明。

- [ ] **Step 4: Commit**

```bash
git add internal/host/sim/fetch_e2e_test.go
git commit -m "test(sim): /fetchsim 真实 URL 冒烟（e2e build tag，手动运行）"
```

- [ ] **Step 5: 收尾**

实现完成后按 superpowers:finishing-a-development-branch 流程走（PR 到 main，`gh pr create` 必须带 `--repo` 与 `--base main`——历史教训见 memory）。

---

## 验证清单（对照 spec）

- [x] 命令 `/fetchsim <作者名> <url>...`，仅空闲 → Task 5
- [x] 作者名目录安全校验 → Task 2（validateAuthorDirName）+ Task 3 测试
- [x] 仅 http/https，逐条独立失败 → Task 3
- [x] 30s 超时 / 20MB 上限 / 浏览器 UA / 重定向（Go 默认 10 次）→ Task 3
- [x] HTML：charset 自动转码 + readability 提取 → Task 2（extractArticle）
- [x] txt：UTF-8 校验 → GB18030 兜底 → Task 2（decodePlainText）
- [x] 其他类型报错跳过 → Task 3（fetchOne default 分支）
- [x] 文件名 = 标题清洗 + URL 短 hash，.txt，覆盖语义 → Task 2 + Task 3 重抓测试
- [x] 质检三项警告不阻断 → Task 2（qualityWarnings）+ Task 3 部分失败测试
- [x] 摘要 + "请检查后运行 /simulate" 提示 → Task 3（StageDone 消息）+ Task 5（doneHint）
- [x] 全部失败以 StageError 收尾 → Task 3
- [x] httptest 单测全覆盖、不碰真实网络 → Task 3
- [x] 真实 URL 冒烟留证据 → Task 7
- [x] user-guide + README + 版权提示 → Task 6
